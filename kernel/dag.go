package kernel

type GraphEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}

type GraphValidation struct {
	Valid  bool    `json:"valid"`
	Reason *string `json:"reason"`
}

func ValidateDAG(nodes []string, edges []GraphEdge) GraphValidation {
	known := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node == "" {
			return invalidGraph("INVALID_NODE")
		}
		if _, exists := known[node]; exists {
			return invalidGraph("DUPLICATE_NODE")
		}
		known[node] = struct{}{}
	}

	children := make(map[string][]string, len(nodes))
	indegree := make(map[string]int, len(nodes))
	for _, node := range nodes {
		indegree[node] = 0
	}
	seenEdges := make(map[GraphEdge]struct{}, len(edges))
	for _, edge := range edges {
		if _, ok := known[edge.Parent]; !ok {
			return invalidGraph("MISSING_NODE")
		}
		if _, ok := known[edge.Child]; !ok {
			return invalidGraph("MISSING_NODE")
		}
		if _, duplicate := seenEdges[edge]; duplicate {
			continue
		}
		seenEdges[edge] = struct{}{}
		children[edge.Parent] = append(children[edge.Parent], edge.Child)
		indegree[edge.Child]++
	}

	queue := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if indegree[node] == 0 {
			queue = append(queue, node)
		}
	}
	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++
		for _, child := range children[node] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(nodes) {
		return invalidGraph("CYCLE")
	}
	return GraphValidation{Valid: true}
}

func invalidGraph(reason string) GraphValidation {
	return GraphValidation{Valid: false, Reason: &reason}
}
