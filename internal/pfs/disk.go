package pfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

func newDisk() *Disk {
	now := time.Now()
	disk := &Disk{
		FormatVersion: diskFormat,
		NextNodeID:    1,
		NextUserID:    1,
		Nodes:         map[string]*Node{},
		Users: []User{
			{ID: rootUserID, Name: "root", PasswordHash: hashPassword("root")},
		},
		Created: now,
		Updated: now,
	}
	base := disk.newNode("base", nodeDir, "", rootUserID)
	disk.RootID = base.ID
	for _, name := range []string{"root", "home", "etc"} {
		child := disk.newNode(name, nodeDir, base.ID, rootUserID)
		base.Children[name] = child.ID
	}
	return disk
}

func (d *Disk) newNode(name, typ, parent string, ownerID int) *Node {
	now := time.Now()
	id := strconv.Itoa(d.NextNodeID)
	d.NextNodeID++
	node := &Node{
		ID:       id,
		Name:     name,
		Type:     typ,
		Parent:   parent,
		OwnerID:  ownerID,
		Created:  now,
		Modified: now,
		Accessed: now,
	}
	if typ == nodeDir {
		node.Children = map[string]string{}
	}
	d.Nodes[id] = node
	return node
}

func loadDisk(name string) (*Disk, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var disk Disk
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, err
	}
	if disk.FormatVersion != diskFormat {
		return nil, fmt.Errorf("unsupported disk format %d", disk.FormatVersion)
	}
	disk.normalize()
	return &disk, nil
}

func (d *Disk) normalize() {
	for _, node := range d.Nodes {
		if node.Type == nodeDir && node.Children == nil {
			node.Children = map[string]string{}
		}
	}
}

func saveDisk(name string, disk *Disk) error {
	disk.Updated = time.Now()
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(name, append(data, '\n'), 0644)
}

func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func (n *Node) size() int {
	if n.Type == nodeFile {
		return len([]byte(n.Content))
	}
	return len(n.Children)
}
