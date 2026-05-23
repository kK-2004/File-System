package pfs

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

func TestNewDiskLayout(t *testing.T) {
	disk := newDisk()
	if disk.RootID == "" {
		t.Fatal("root id is empty")
	}
	root := disk.Nodes[disk.RootID]
	for _, name := range []string{"root", "home", "etc"} {
		if _, ok := root.Children[name]; !ok {
			t.Fatalf("missing initial directory %q", name)
		}
	}
	if len(disk.Users) != 1 || disk.Users[0].Name != "root" {
		t.Fatalf("unexpected users: %#v", disk.Users)
	}
}

func TestResolveAndPwd(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
	}
	rootDir, err := session.mustDir("/root")
	if err != nil {
		t.Fatal(err)
	}
	session.currentID = rootDir.ID
	if got := session.pwd(); got != "/root" {
		t.Fatalf("pwd = %q, want /root", got)
	}
	if err := session.mkdir([]string{"demo"}); err != nil {
		t.Fatal(err)
	}
	node, err := session.resolve("/root/demo")
	if err != nil {
		t.Fatal(err)
	}
	if node.Name != "demo" || node.Type != nodeDir {
		t.Fatalf("resolved unexpected node: %#v", node)
	}
}

func TestLoginRetryAfterWrongPassword(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:   disk,
		reader: bufio.NewReader(bytes.NewBufferString("root\nwrong\nr\nroot\n")),
		out:    &bytes.Buffer{},
	}

	if err := session.login(""); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if session.currentUser.Name != "root" {
		t.Fatalf("current user = %q, want root", session.currentUser.Name)
	}
}

func TestLoginCanReturnToUsernamePrompt(t *testing.T) {
	disk := newDisk()
	disk.Users = append(disk.Users, User{
		ID:           disk.NextUserID,
		Name:         "alice",
		PasswordHash: hashPassword("alice123"),
	})
	disk.NextUserID++

	home := disk.Nodes[disk.RootID].Children["home"]
	homeNode := disk.Nodes[home]
	userDir := disk.newNode("alice", nodeDir, homeNode.ID, 1)
	homeNode.Children["alice"] = userDir.ID

	session := &Session{
		disk:   disk,
		reader: bufio.NewReader(bytes.NewBufferString("root\nwrong\nu\nalice\nalice123\n")),
		out:    &bytes.Buffer{},
	}

	if err := session.login(""); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if session.currentUser.Name != "alice" {
		t.Fatalf("current user = %q, want alice", session.currentUser.Name)
	}
}

func TestLoginCanRecoverFromUnknownUsername(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:   disk,
		reader: bufio.NewReader(bytes.NewBufferString("admin\nu\nroot\nroot\n")),
		out:    &bytes.Buffer{},
	}

	if err := session.login(""); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if session.currentUser.Name != "root" {
		t.Fatalf("current user = %q, want root", session.currentUser.Name)
	}
}

func TestLoginCanExitAfterUnknownUsername(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:   disk,
		reader: bufio.NewReader(bytes.NewBufferString("admin\ne\n")),
		out:    &bytes.Buffer{},
	}

	err := session.login("")
	if !errors.Is(err, errLoginExit) {
		t.Fatalf("login error = %v, want %v", err, errLoginExit)
	}
}
