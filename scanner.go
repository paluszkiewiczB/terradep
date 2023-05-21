package terradep

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/gohcl"
	"go.interactor.dev/terradep/inspect"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// Scanner can scan the directories looking for a Terraform projects
type Scanner struct {
	skipDirs map[string]struct{}
}

// NewScanner returns initialized instance of Scanner
func NewScanner(globs map[string]struct{}) *Scanner {
	if len(globs) == 0 {
		globs = defaultSkips
	}

	return &Scanner{skipDirs: globs}
}

var defaultSkips = map[string]struct{}{".terraform": {}, ".idea": {}, ".vscode": {}, ".external_modules": {}}

// Scan recursively scans the root directory and tries to find Terraform modules
func (s *Scanner) Scan(root string) (*Graph, error) {
	if err := checkIfDirExists(root); err != nil {
		return nil, err
	}

	modDeps := map[string][]State{}
	modStates := map[string]State{}
	err := filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if info != nil && !info.IsDir() {
			// skip files, we only care about directories
			return nil
		}

		if _, ok := s.skipDirs[info.Name()]; ok {
			return fs.SkipDir
		}

		if !tfconfig.IsModuleDir(path) {
			log.Printf("not a module dir: %s", path)
			return nil
		}

		log.Printf("loading module from path: %q", path)

		module, diag := tfconfig.LoadModule(path)
		if diag.HasErrors() {
			return fmt.Errorf("loading module: %q, %w", path, err)
		}

		dependencies, err := findDependencies(module)
		if err != nil {
			return fmt.Errorf("finding dependencies in module: %s, %w", path, err)
		}
		modDeps[module.Path] = dependencies

		tfState, err := findState(module)
		if err != nil {
			return fmt.Errorf("find state in module: %s, %w", path, err)
		}
		modStates[path] = tfState

		// do not scan submodules
		return fs.SkipDir
	})
	if err != nil {
		return nil, err
	}

	return buildTree(modStates, modDeps), nil
}

func buildTree(states map[string]State, deps map[string][]State) *Graph {
	log.Printf("building dependency tree")

	for path, state := range states {
		log.Printf("module: %s has state: %v", path, state)
	}

	for path, states := range deps {
		log.Printf("module: %s has %d dependencies", path, len(states))
	}

	nodes := make([]*Node, 0, len(states))
	for path, state := range states {
		nodes = append(nodes, &Node{
			Path:  path,
			State: state,
		})
	}

	nodesByPath := groupByPath(nodes)
	nodesByState := groupByState(nodes)

	for parentPath, modDeps := range deps {
		parentNode := nodesByPath[parentPath]
		for _, childState := range modDeps {
			childNode, ok := nodesByState[childState]
			if !ok {
				// this is external module - not known to the scanner - it will never have children and path
				log.Printf("found external module with state: %s", childState)
				childNode = &Node{
					State: childState,
				}
			}

			parentNode.Children = append(parentNode.Children, childNode)
			childNode.Parent = parentNode
		}
	}

	roots := make([]*Node, 0)
	for _, node := range nodes {
		// roots are nodes without dependencies
		if node.Parent == nil {
			roots = append(roots, node)
		}
	}

	if len(roots) == 0 {
		panic("none of the modules is independent")
	}

	return &Graph{Heads: roots}
}

func groupByPath(nodes []*Node) map[string]*Node {
	out := make(map[string]*Node, len(nodes))
	for _, node := range nodes {
		if ex, duplicate := out[node.Path]; duplicate {
			panic(fmt.Errorf("more than one node has the same path: %q, first node: %v, second node: %v", node.Path, *ex, *node))
		}

		out[node.Path] = node
	}

	return out
}

func groupByState(nodes []*Node) map[State]*Node {
	out := make(map[State]*Node, len(nodes))
	for _, node := range nodes {
		if ex, duplicate := out[node.State]; duplicate {
			panic(fmt.Errorf("more than one node has the same state: %v, first node: %v, second node: %v", node.State, *ex, *node))
		}

		out[node.State] = node
	}

	return out
}

func findDependencies(module *tfconfig.Module) (out []State, err error) {
	remoteStates := make([]*tfconfig.Resource, 0)
	for _, resource := range module.DataResources {
		if resource.Type == "terraform_remote_state" {
			remoteStates = append(remoteStates, resource)
		}
	}

	for file, resources := range groupResByFile(remoteStates) {
		// grouping allows to parse file only once
		states, err := parseTerraformRemoteStates(file, resources)
		if err != nil {
			return nil, err
		}

		out = append(out, states...)
	}

	return
}

/*
example:

	//data "terraform_remote_state" "domain_data" {
	  backend = "s3"

	  config = {
		bucket  = "your-bucket"
		key     = "terraform/domain/deployment/tfstate.json"
		region  = "eu-west-3"
		encrypt = true
	  }
	}
*/
type remoteState struct {
	Backend string           `hcl:"backend"`
	Config  *s3BackendConfig `hcl:"config"`
}

type s3BackendConfig struct {
	Bucket  string    `hcl:"bucket" cty:"bucket"`
	Key     string    `hcl:"key" cty:"key"`
	Region  string    `hcl:"region" cty:"region"`
	Encrypt bool      `hcl:"encrypt,option" cty:"encrypt"`
	Remain  *hcl.Body `hcl:"remain"`
}

func parseTerraformRemoteStates(file string, resources []*tfconfig.Resource) ([]State, error) {
	parser := hclparse.NewParser()
	hclFile, diags := parser.ParseHCLFile(file)
	if diags.HasErrors() {
		return nil, diags
	}

	content, _, diags := hclFile.Body.PartialContent(backendSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	remoteStates := make([]*remoteState, 0, len(resources))
	for _, block := range content.Blocks {
		const trs = "terraform_remote_state"
		if resType := block.Labels[0]; resType != trs {
			log.Printf("skip block because first label is not: %q, but: %q", trs, resType)
			continue
		}

		stateName := block.Labels[1]
		if len(stateName) == 0 {
			return nil, fmt.Errorf("block %q does not have the name", trs)
		}

		state := &remoteState{}
		diags := gohcl.DecodeBody(block.Body, nil, state)
		if diags.HasErrors() {
			return nil, fmt.Errorf("decoding block body to remoteState: %w", diags)
		}

		log.Printf("decoded remote state: %v", *state)
		remoteStates = append(remoteStates, state)
	}

	if len(remoteStates) != len(resources) {
		return nil, fmt.Errorf("expected to parse: %d remote states, but found: %d", len(resources), len(remoteStates))
	}

	states := make([]State, 0, len(remoteStates))
	for _, state := range remoteStates {
		// TODO support more backends by injectable mappers
		if state.Backend != "s3" {
			return nil, fmt.Errorf("unsupported backend type: %q, must be 's3'", state.Backend)
		}

		states = append(states, S3State{
			Bucket: state.Config.Bucket,
			Key:    state.Config.Key,
		})
	}

	return states, nil
}

// groupResByFiles accepts map of resources, ignores the key and returns map where key is file containing the resources
func groupResByFile(res []*tfconfig.Resource) map[string][]*tfconfig.Resource {
	out := map[string][]*tfconfig.Resource{}

	for _, resource := range res {
		key := resource.Pos.Filename
		out[key] = append(out[key], resource)
	}

	return out
}

/*
example:

	terraform {
	  required_version = "1.2.7"

	  backend "s3" {
		bucket  = "your-bucket"
		key     = "terraform/domain/deployment/tfstate.json"
		region  = "eu-west-3"
	    encrypt = true
	  }
	}
*/
// FIXME this is s3-specific, should be generified once more types of backend is supported
type terraformBlock struct {
	Version string                   `hcl:"required_version,attr" cty:"required_version,attr"`
	Backend *terraformBlockS3Backend `hcl:"backend,block"`

	// Remain stores unused part of the body, e.g. required_providers
	Remain hcl.Body `hcl:",remain"`
}

type terraformBlockS3Backend struct {
	Type    string `hcl:"type,label" cty:"type,label"`
	Bucket  string `hcl:"bucket" cty:"bucket"`
	Key     string `hcl:"key" cty:"key"`
	Region  string `hcl:"region" cty:"region"`
	Encrypt bool   `hcl:"encrypt" cty:"encrypt"`
}

func (b terraformBlockS3Backend) toState() S3State {
	return S3State{
		Bucket: b.Bucket,
		Key:    b.Key,
	}
}

func findState(mod *tfconfig.Module) (State, error) {
	block, err := inspect.FindTerraformBlock(mod.Path)
	if err != nil {
		return nil, fmt.Errorf("finding terraform block for in module: %s, %w", mod.Path, err)
	}

	tb := &terraformBlock{}
	diags := gohcl.DecodeBody(block.Body, nil, tb)
	if diags.HasErrors() {
		return nil, fmt.Errorf("decoding terraform block to object: %w", diags)
	}

	return tb.Backend.toState(), nil
}

func checkIfDirExists(path string) error {
	stat, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("path does not exist: %s", path)
	case err != nil:
		return err
	}

	if !stat.IsDir() {
		return fmt.Errorf("it is not directory: %s", path)
	}
	return nil
}

// Graph is acyclic directed graph showing dependencies between Terraform states
type Graph struct {
	// Heads are Nodes which represent Terraform deployments without dependencies to other states
	Heads []*Node
}

// String is insanely poor implementation of representing the Graph in JSON lines format.
// Assumes Node.String returns a JSON and concatenates them
func (g *Graph) String() string {
	sb := strings.Builder{}
	sb.WriteRune('\n')
	for _, head := range g.Heads {
		sb.WriteString(head.String())
		sb.WriteRune('\n')
	}

	return sb.String()
}

// Node represents Terraform deployment
type Node struct {
	Path     string
	State    State
	Parent   *Node
	Children []*Node
}

func (n *Node) String() string {
	sb := strings.Builder{}
	sb.WriteString("{\"name\":\"")
	sb.WriteString(n.State.String())
	sb.WriteString("\"")
	if len(n.Children) != 0 {
		sb.WriteString(",\"children\":[")
		for i, child := range n.Children {
			sb.WriteString(child.String())
			if i != len(n.Children)-1 {
				sb.WriteRune(',')
			}
		}
		sb.WriteString("]")
	}
	sb.WriteString("}")
	return sb.String()
}

// State is used as unique identifier of Terraform state referenced by [terraform_remote_state] or in attribute [backend] in terraform block
//
// [terraform_remote_state]: https://developer.hashicorp.com/terraform/language/state/remote
// [backend]: https://developer.hashicorp.com/terraform/language/settings/backends/configuration#using-a-backend-block
type State fmt.Stringer

// S3State represents Terraform state stored in S3 bucket
type S3State struct {
	// Bucket is name of S3 bucket
	Bucket string
	// Bucket key of the object in S3 bucket
	Key string
}

// String implements State
func (s S3State) String() string {
	return fmt.Sprintf("s3://%s/%s", s.Bucket, s.Key)
}

var backendSchema = &hcl.BodySchema{
	Blocks:     []hcl.BlockHeaderSchema{{Type: "data", LabelNames: []string{"type", "name"}}},
	Attributes: []hcl.AttributeSchema{{Name: "backend"}, {Name: "config"}},
}
