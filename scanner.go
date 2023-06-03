package terradep

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/hcl/v2/gohcl"
	"go.interactor.dev/terradep/inspect"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// State is used as unique identifier of Terraform state referenced by [terraform_remote_state] or in attribute [backend] in terraform block
//
// [terraform_remote_state]: https://developer.hashicorp.com/terraform/language/state/remote
// [backend]: https://developer.hashicorp.com/terraform/language/settings/backends/configuration#using-a-backend-block
type State fmt.Stringer

// Scanner can scan the directories looking for a Terraform projects
type Scanner struct {
	skipDirs map[string]struct{}
	stater   Stater
}

// Stater can read the state from attribute [backend] in terraform block or [terraform_remote_state]
//
// [backend]: https://developer.hashicorp.com/terraform/language/settings/backends/configuration#using-a-backend-block
// [terraform_remote_state]: https://developer.hashicorp.com/terraform/language/state/remote
type Stater interface {
	BackendState(backend string, body hcl.Body) (State, error)
	RemoteState(backend string, config map[string]cty.Value) (State, error)
}

// NewScanner returns initialized instance of Scanner
func NewScanner(stater Stater, opts ...ScannerOpt) *Scanner {
	cfg := &scannerCfg{
		globs:      DefaultSkipDirs,
		extraGlobs: nil,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	return &Scanner{
		stater:   stater,
		skipDirs: cfg.mergeGlobs(),
	}
}

// ScannerOpt is used by [NewScanner] to change behaviour of created [Scanner]
type ScannerOpt func(cfg *scannerCfg)

// SetSkipDirs specifies which directories must be skipped by the [Scanner].
// If not set, defaults to [DefaultSkipDirs]
func SetSkipDirs(dirs []string) ScannerOpt {
	return func(cfg *scannerCfg) {
		cfg.globs = dirs
	}
}

// AddSkipDirs adds more dirs to be skipped. It can extend dirs set with [SetSkipDirs] or [DefaultSkipDirs]
func AddSkipDirs(dirs []string) ScannerOpt {
	return func(cfg *scannerCfg) {
		cfg.extraGlobs = append(cfg.extraGlobs, dirs...)
	}
}

type scannerCfg struct {
	globs      []string
	extraGlobs []string
}

func (c scannerCfg) mergeGlobs() map[string]struct{} {
	out := make(map[string]struct{}, 0)
	for _, dir := range c.globs {
		out[dir] = struct{}{}
	}
	for _, dir := range c.extraGlobs {
		out[dir] = struct{}{}
	}

	return out
}

// DefaultSkipDirs is a slice of directories skipped by a [Scanner] by default when creating it with [NewScanner]
// It can be overridden with [SetSkipDirs] or extended with [AddSkipDirs]
var DefaultSkipDirs = []string{".terraform", ".idea", ".vscode", ".external_modules"}

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

		log.Printf("loading module from path: %s", path)

		module, diag := tfconfig.LoadModule(path)
		if diag.HasErrors() {
			return fmt.Errorf("loading module: %q, %w", path, err)
		}

		dependencies, err := s.findDependencies(module)
		if err != nil {
			return fmt.Errorf("finding dependencies in module: %s, %w", path, err)
		}
		modDeps[module.Path] = dependencies

		tfState, err := s.findState(module)
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

func (s *Scanner) findDependencies(module *tfconfig.Module) (out []State, err error) {
	remoteStates := make([]*tfconfig.Resource, 0)
	for _, resource := range module.DataResources {
		if resource.Type == "terraform_remote_state" {
			remoteStates = append(remoteStates, resource)
		}
	}

	for file, resources := range groupResByFile(remoteStates) {
		// grouping allows to parse file only once
		states, err := s.parseTerraformRemoteStates(file, resources)
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
	  backend = "someBackendType"

	  config = {
		some = "data"
	  }
	}
*/
type remoteState struct {
	Backend string         `hcl:"backend"`
	Config  hcl.Attributes `hcl:",remain"`
}

func (s *Scanner) parseTerraformRemoteStates(file string, resources []*tfconfig.Resource) ([]State, error) {
	parser := hclparse.NewParser()
	hclFile, diags := parser.ParseHCLFile(file)
	if diags.HasErrors() {
		return nil, diags
	}

	content, _, diags := hclFile.Body.PartialContent(backendSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	remoteStates := make([]State, 0, len(resources))
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

		backend, backendCfg, err := parseRemoteState(block)
		if err != nil {
			return nil, fmt.Errorf("parsing terraform remote state, %w", err)
		}

		state, err := s.stater.RemoteState(backend, backendCfg)
		if err != nil {
			return nil, fmt.Errorf("reading state from terraform_remote_state: %q, %w", stateName, err)
		}

		log.Printf("decoded remote state: %s", state)
		remoteStates = append(remoteStates, state)
	}

	if len(remoteStates) != len(resources) {
		return nil, fmt.Errorf("expected to parse: %d remote states, but found: %d", len(resources), len(remoteStates))
	}

	return remoteStates, nil
}

func parseRemoteState(block *hcl.Block) (backend string, cfg map[string]cty.Value, err error) {
	rs := &remoteState{}
	diags := gohcl.DecodeBody(block.Body, nil, rs)
	if diags.HasErrors() {
		return "", nil, fmt.Errorf("decoding block body to remoteState: %w", diags)
	}

	value, diags := rs.Config["config"].Expr.Value(nil)
	if diags.HasErrors() {
		return "", nil, fmt.Errorf("reading value of remote state config, %w", diags)
	}
	if !value.Type().IsObjectType() {
		return "", nil, fmt.Errorf("terraform remote state config must be an object")
	}

	return rs.Backend, value.AsValueMap(), nil
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

	  backend "someBackend" {
		some = "data"
		other = ["list"]
	  }
	}
*/
type terraformBlock struct {
	Version string `hcl:"required_version,attr" cty:"required_version,attr"`
	Backend struct {
		Type string   `hcl:"type,label" cty:"type,label"`
		Body hcl.Body `hcl:",remain"`
	} `hcl:"backend,block"`

	// Remain stores unused part of the body, e.g. required_providers
	Remain hcl.Body `hcl:",remain"`
}

func (s *Scanner) findState(mod *tfconfig.Module) (State, error) {
	block, err := inspect.FindTerraformBlock(mod.Path)
	if err != nil {
		return nil, fmt.Errorf("finding terraform block for in module: %s, %w", mod.Path, err)
	}

	tb := &terraformBlock{}
	diags := gohcl.DecodeBody(block.Body, nil, tb)
	if diags.HasErrors() {
		return nil, fmt.Errorf("decoding terraform block to object: %w", diags)
	}

	return s.stater.BackendState(tb.Backend.Type, tb.Backend.Body)
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

var backendSchema = &hcl.BodySchema{
	Blocks:     []hcl.BlockHeaderSchema{{Type: "data", LabelNames: []string{"type", "name"}}},
	Attributes: []hcl.AttributeSchema{{Name: "backend"}, {Name: "config"}},
}
