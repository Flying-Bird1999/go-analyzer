package graph

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type EvidenceChain struct {
	ID    string         `json:"id"`
	Nodes []EvidenceNode `json:"nodes"`
	Edges []EvidenceEdge `json:"edges"`
}

type EvidenceNode struct {
	ID     string           `json:"id"`
	Reason string           `json:"reason"`
	Span   facts.SourceSpan `json:"span,omitempty"`
}

type EvidenceEdge struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Reason string `json:"reason"`
}

func NewEvidenceChain(id string) EvidenceChain {
	return EvidenceChain{ID: id, Nodes: []EvidenceNode{}, Edges: []EvidenceEdge{}}
}

func (c *EvidenceChain) AddNode(id, reason string, span facts.SourceSpan) {
	for _, node := range c.Nodes {
		if node.ID == id {
			return
		}
	}
	c.Nodes = append(c.Nodes, EvidenceNode{ID: id, Reason: reason, Span: span})
}

func (c *EvidenceChain) AddEdge(fromID, toID, reason string) {
	c.Edges = append(c.Edges, EvidenceEdge{FromID: fromID, ToID: toID, Reason: reason})
}
