package pfs

import (
	"bufio"
	"io"
	"time"
)

const (
	nodeFile = "file"
	nodeDir  = "dir"

	rootUserID = 0
	diskFormat = 1
)

type User struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash"`
}

type Node struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Parent   string            `json:"parent,omitempty"`
	OwnerID  int               `json:"owner_id"`
	Children map[string]string `json:"children,omitempty"`
	Content  string            `json:"content,omitempty"`
	Created  time.Time         `json:"created"`
	Modified time.Time         `json:"modified"`
	Accessed time.Time         `json:"accessed"`
}

type Disk struct {
	FormatVersion int              `json:"format_version"`
	NextNodeID    int              `json:"next_node_id"`
	NextUserID    int              `json:"next_user_id"`
	RootID        string           `json:"root_id"`
	Nodes         map[string]*Node `json:"nodes"`
	Users         []User           `json:"users"`
	Created       time.Time        `json:"created"`
	Updated       time.Time        `json:"updated"`
}

type Session struct {
	disk        *Disk
	diskPath    string
	currentID   string
	currentUser User
	reader      *bufio.Reader
	out         io.Writer
}
