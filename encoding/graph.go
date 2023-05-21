package encoding

import (
	"fmt"

	"go.interactor.dev/terradep"
	multi2 "gonum.org/v1/gonum/graph/multi"
)
import "gonum.org/v1/gonum/graph/encoding/dot"

// BuildDOTGraph returns graph represented in Graphviz DOT format
func BuildDOTGraph(dep *terradep.Graph) ([]byte, error) {
	multi := multi2.NewDirectedGraph()

	nodeByPath := mapNodes(dep)

	for _, node := range nodeByPath {
		for _, child := range node.Children {
			line := multi.NewLine(node, nodeByPath[child.Path])
			multi.SetLine(line)
		}
	}

	bytes, err := dot.MarshalMulti(multi, "name", "", "")
	if err != nil {
		return nil, fmt.Errorf("marshaling multigraph: %w", err)
	}

	return bytes, nil
}

// mapNodes returns map where key is path to the module of terradep.Node
func mapNodes(dep *terradep.Graph) map[string]graphNode {
	depNodes := make([]*terradep.Node, 0)
	for _, head := range dep.Heads {
		depNodes = append(depNodes, head)
		depNodes = append(depNodes, getAllChildren(head)...)
	}

	uniqueDepNodes := toGraphNodes(depNodes)

	out := make(map[string]graphNode, len(uniqueDepNodes))
	for _, depNode := range uniqueDepNodes {
		out[depNode.Path] = depNode
	}

	return out
}

func getAllChildren(n *terradep.Node) []*terradep.Node {
	if len(n.Children) == 0 {
		return nil
	}

	var out []*terradep.Node
	out = append(out, n.Children...)

	for _, child := range n.Children {
		out = append(out, getAllChildren(child)...)
	}

	return out
}

func toGraphNodes(nodes []*terradep.Node) []graphNode {
	out := make([]graphNode, 0)
	for i, node := range nodes {
		out = append(out, graphNode{
			id:   int64(i),
			Node: node,
		})
	}

	return out
}

type graphNode struct {
	id int64
	*terradep.Node
}

// ID implements graph.Node
func (n graphNode) ID() int64 {
	return n.id
}

// DOTID implements dot.Node
func (n graphNode) DOTID() string {
	return n.State.String()
}
