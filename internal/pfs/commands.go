package pfs

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func (s *Session) mkdir(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: mkdir dirname")
	}
	parent := s.cwd()
	if err := s.canWrite(parent); err != nil {
		return err
	}
	name := args[0]
	if err := validateName(name); err != nil {
		return err
	}
	if _, exists := parent.Children[name]; exists {
		return errors.New("name already exists")
	}
	child := s.disk.newNode(name, nodeDir, parent.ID, s.currentUser.ID)
	parent.Children[name] = child.ID
	parent.Modified = now()
	return nil
}

func (s *Session) touch(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: touch filename")
	}
	parent := s.cwd()
	if err := s.canWrite(parent); err != nil {
		return err
	}
	name := args[0]
	if err := validateName(name); err != nil {
		return err
	}
	if id, exists := parent.Children[name]; exists {
		s.disk.Nodes[id].Modified = now()
		return nil
	}
	child := s.disk.newNode(name, nodeFile, parent.ID, s.currentUser.ID)
	parent.Children[name] = child.ID
	parent.Modified = now()
	return nil
}

func (s *Session) write(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vim filename [content...]")
	}
	file, err := s.fileOrCreate(args[0])
	if err != nil {
		return err
	}
	if err := s.canWrite(file); err != nil {
		return err
	}
	if len(args) > 1 {
		file.Content = strings.Join(args[1:], " ")
	} else {
		fmt.Fprintln(s.out, "enter text; finish with a single dot on its own line")
		lines := []string{}
		for {
			text, err := s.reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			line := strings.TrimRight(text, "\r\n")
			if line == "." || errors.Is(err, io.EOF) {
				break
			}
			lines = append(lines, line)
		}
		file.Content = strings.Join(lines, "\n")
	}
	markChanged(file)
	return nil
}

func (s *Session) more(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: more filename")
	}
	node, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	if node.Type != nodeFile {
		return errors.New("not a file")
	}
	if err := s.canRead(node); err != nil {
		return err
	}
	node.Accessed = now()
	fmt.Fprintln(s.out, node.Content)
	return nil
}

func (s *Session) cd(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cd path")
	}
	node, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	if node.Type != nodeDir {
		return errors.New("not a directory")
	}
	if err := s.canRead(node); err != nil {
		return err
	}
	s.currentID = node.ID
	node.Accessed = now()
	return nil
}

func (s *Session) cp(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: cp src_file target_dir")
	}
	src, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	if src.Type != nodeFile {
		return errors.New("only file copy is supported")
	}
	if err := s.canRead(src); err != nil {
		return err
	}
	target, err := s.resolve(args[1])
	if err != nil {
		return err
	}
	if target.Type != nodeDir {
		return errors.New("target is not a directory")
	}
	if err := s.canWrite(target); err != nil {
		return err
	}
	if _, exists := target.Children[src.Name]; exists {
		return errors.New("target already contains that name")
	}
	copyNode := s.disk.newNode(src.Name, nodeFile, target.ID, s.currentUser.ID)
	copyNode.Content = src.Content
	target.Children[copyNode.Name] = copyNode.ID
	target.Modified = now()
	return nil
}

func (s *Session) mv(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: mv src target_dir")
	}
	src, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	target, err := s.resolve(args[1])
	if err != nil {
		return err
	}
	if target.Type != nodeDir {
		return errors.New("target is not a directory")
	}
	if src.ID == s.disk.RootID || src.ID == target.ID {
		return errors.New("invalid move")
	}
	oldParent := s.disk.Nodes[src.Parent]
	if err := s.canWrite(oldParent); err != nil {
		return err
	}
	if err := s.canWrite(target); err != nil {
		return err
	}
	if _, exists := target.Children[src.Name]; exists {
		return errors.New("target already contains that name")
	}
	delete(oldParent.Children, src.Name)
	target.Children[src.Name] = src.ID
	src.Parent = target.ID
	timestamp := now()
	oldParent.Modified = timestamp
	target.Modified = timestamp
	src.Modified = timestamp
	return nil
}

func (s *Session) rename(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: rename old new")
	}
	parent := s.cwd()
	if err := s.canWrite(parent); err != nil {
		return err
	}
	if err := validateName(args[1]); err != nil {
		return err
	}
	id, exists := parent.Children[args[0]]
	if !exists {
		return errors.New("file not found")
	}
	if _, duplicate := parent.Children[args[1]]; duplicate {
		return errors.New("name already exists")
	}
	delete(parent.Children, args[0])
	parent.Children[args[1]] = id
	node := s.disk.Nodes[id]
	node.Name = args[1]
	node.Modified = now()
	parent.Modified = node.Modified
	return nil
}

func (s *Session) tree(args []string) error {
	depth := -1
	if len(args) == 2 && args[0] == "-d" {
		value, err := strconv.Atoi(args[1])
		if err != nil || value < 1 {
			return errors.New("invalid depth")
		}
		depth = value
	} else if len(args) > 0 {
		return errors.New("usage: tree [-d depth]")
	}
	s.printTree(s.cwd(), depth, 0)
	return nil
}

func (s *Session) printTree(node *Node, depth, level int) {
	if depth == 0 || node.Type != nodeDir {
		return
	}
	nextDepth := depth
	if nextDepth > 0 {
		nextDepth--
	}
	for _, child := range s.sortedChildren(node) {
		fmt.Fprintf(s.out, "%s├── %s\n", strings.Repeat("│   ", level), child.Name)
		if child.Type == nodeDir {
			s.printTree(child, nextDepth, level+1)
		}
	}
}

func (s *Session) ls(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: ls")
	}
	names := []string{}
	for _, child := range s.sortedChildren(s.cwd()) {
		names = append(names, child.Name)
	}
	fmt.Fprintln(s.out, strings.Join(names, " "))
	return nil
}

func (s *Session) ll(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: ll")
	}
	for _, child := range s.sortedChildren(s.cwd()) {
		fmt.Fprintf(s.out, "%-4s %-12s owner=%d size=%d modified=%s\n",
			child.Type, child.Name, child.OwnerID, child.size(), child.Modified.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func (s *Session) stat(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: stat name")
	}
	node, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "id: %s\nname: %s\ntype: %s\nowner: %d\nsize: %d\ncreated: %s\naccessed: %s\nmodified: %s\n",
		node.ID, node.Name, node.Type, node.OwnerID, node.size(),
		node.Created.Format(time.RFC3339), node.Accessed.Format(time.RFC3339), node.Modified.Format(time.RFC3339))
	return nil
}

func (s *Session) detail() {
	files := 0
	dirs := 0
	for _, node := range s.disk.Nodes {
		if node.Type == nodeDir {
			dirs++
		} else {
			files++
		}
	}
	fmt.Fprintf(s.out, "File System format: %d\nusers: %d\ndirectories: %d\nfiles: %d\ndisk: %s\n",
		s.disk.FormatVersion, len(s.disk.Users), dirs, files, s.diskPath)
}

func (s *Session) rm(args []string) error {
	recursive := false
	if len(args) == 2 && args[0] == "-r" {
		recursive = true
		args = args[1:]
	}
	if len(args) != 1 {
		return errors.New("usage: rm [-r] name")
	}
	node, err := s.resolve(args[0])
	if err != nil {
		return err
	}
	if node.ID == s.disk.RootID {
		return errors.New("cannot remove root")
	}
	if node.Type == nodeDir && !recursive {
		return errors.New("cannot remove directory without -r")
	}
	parent := s.disk.Nodes[node.Parent]
	if err := s.canWrite(parent); err != nil {
		return err
	}
	s.removeSubtree(node)
	delete(parent.Children, node.Name)
	parent.Modified = now()
	if s.currentID == node.ID {
		s.currentID = parent.ID
	}
	return nil
}

func (s *Session) removeSubtree(node *Node) {
	if node.Type == nodeDir {
		for _, id := range node.Children {
			s.removeSubtree(s.disk.Nodes[id])
		}
	}
	delete(s.disk.Nodes, node.ID)
}

func now() time.Time {
	return time.Now()
}

func markChanged(node *Node) {
	timestamp := now()
	node.Modified = timestamp
	node.Accessed = timestamp
}
