package pfs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (s *Session) fileOrCreate(name string) (*Node, error) {
	node, err := s.resolve(name)
	if err == nil {
		if node.Type != nodeFile {
			return nil, errors.New("not a file")
		}
		return node, nil
	}
	if strings.Contains(name, "/") {
		return nil, err
	}
	if err := s.touch([]string{name}); err != nil {
		return nil, err
	}
	return s.resolve(name)
}

func (s *Session) resolve(input string) (*Node, error) {
	if input == "" {
		return s.cwd(), nil
	}
	current := s.cwd()
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, "~") {
		current = s.disk.Nodes[s.disk.RootID]
		input = strings.TrimPrefix(input, "/")
		input = strings.TrimPrefix(input, "~")
		input = strings.TrimPrefix(input, "/")
	}
	if input == "" {
		return current, nil
	}
	for _, part := range strings.Split(input, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if current.Parent != "" {
				current = s.disk.Nodes[current.Parent]
			}
			continue
		}
		if current.Type != nodeDir {
			return nil, fmt.Errorf("%s is not a directory", current.Name)
		}
		id, ok := current.Children[part]
		if !ok {
			return nil, fmt.Errorf("%s not found", part)
		}
		current = s.disk.Nodes[id]
	}
	return current, nil
}

func (s *Session) mustDir(input string) (*Node, error) {
	node, err := s.resolve(input)
	if err != nil {
		return nil, err
	}
	if node.Type != nodeDir {
		return nil, fmt.Errorf("%s is not a directory", input)
	}
	return node, nil
}

func (s *Session) cwd() *Node {
	return s.disk.Nodes[s.currentID]
}

func (s *Session) pwd() string {
	parts := []string{}
	for node := s.cwd(); node != nil; node = s.disk.Nodes[node.Parent] {
		parts = append(parts, node.Name)
		if node.Parent == "" {
			break
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return "/" + strings.Join(parts[1:], "/")
}

func (s *Session) canRead(node *Node) error {
	if s.currentUser.ID == rootUserID || s.currentUser.ID == node.OwnerID {
		return nil
	}
	if node.Type == nodeDir && (node.Name == "home" || node.Name == "etc") {
		return nil
	}
	return errors.New("permission denied")
}

func (s *Session) canWrite(node *Node) error {
	if s.currentUser.ID == rootUserID || s.currentUser.ID == node.OwnerID {
		return nil
	}
	return errors.New("permission denied")
}

func (s *Session) sortedChildren(node *Node) []*Node {
	children := make([]*Node, 0, len(node.Children))
	for _, id := range node.Children {
		children = append(children, s.disk.Nodes[id])
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].Type != children[j].Type {
			return children[i].Type == nodeDir
		}
		return children[i].Name < children[j].Name
	})
	return children
}

func validateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return errors.New("invalid name")
	}
	return nil
}
