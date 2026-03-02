package graph

import "sort"

type Task struct {
	ID         string   `json:"id"`
	Params     []string `json:"params"`
	ReturnType string   `json:"return_type"`
	Deps       []string `json:"deps"`
}

type Graph struct {
	Nodes map[string]Task
}

func New(tasks []Task) Graph {
	nodes := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		dependencies := make([]string, 0, len(task.Deps))
		for _, dependency := range task.Deps {
			dependencies = append(dependencies, dependency)
		}
		sort.Strings(dependencies)

		nodes[task.ID] = Task{
			ID:         task.ID,
			Params:     append([]string{}, task.Params...),
			ReturnType: task.ReturnType,
			Deps:       dependencies,
		}
	}
	return Graph{Nodes: nodes}
}

func (g Graph) SortedTaskIDs() []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (g Graph) DetectCycle() ([]string, bool) {
	visiting := make(map[string]bool, len(g.Nodes))
	visited := make(map[string]bool, len(g.Nodes))
	stack := make([]string, 0, len(g.Nodes))

	var walk func(string) ([]string, bool)
	walk = func(id string) ([]string, bool) {
		if visiting[id] {
			cycleStart := 0
			for cycleStart < len(stack) && stack[cycleStart] != id {
				cycleStart++
			}
			cycle := append([]string{}, stack[cycleStart:]...)
			cycle = append(cycle, id)
			return cycle, true
		}
		if visited[id] {
			return nil, false
		}

		task, exists := g.Nodes[id]
		if !exists {
			return nil, false
		}

		visiting[id] = true
		stack = append(stack, id)

		for _, dependency := range task.Deps {
			cycle, hasCycle := walk(dependency)
			if hasCycle {
				return cycle, true
			}
		}

		stack = stack[:len(stack)-1]
		visiting[id] = false
		visited[id] = true
		return nil, false
	}

	for _, id := range g.SortedTaskIDs() {
		cycle, hasCycle := walk(id)
		if hasCycle {
			return cycle, true
		}
	}

	return nil, false
}
