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

func TestFileContentAllocatesAndReleasesBlocks(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
		reader:      bufio.NewReader(bytes.NewBuffer(nil)),
		out:         &bytes.Buffer{},
	}

	initialFree := len(disk.FreeBlocks)
	if err := session.write([]string{"a.txt", "hello"}); err != nil {
		t.Fatal(err)
	}
	file, err := session.resolve("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(file.BlockIDs); got != 1 {
		t.Fatalf("file blocks = %d, want 1", got)
	}
	if got := len(disk.FreeBlocks); got != initialFree-1 {
		t.Fatalf("free blocks after write = %d, want %d", got, initialFree-1)
	}

	if err := session.write([]string{"a.txt", "hello", "again"}); err != nil {
		t.Fatal(err)
	}
	if got := len(file.BlockIDs); got != 1 {
		t.Fatalf("file blocks after rewrite = %d, want 1", got)
	}
	if got := len(disk.FreeBlocks); got != initialFree-1 {
		t.Fatalf("free blocks after rewrite = %d, want %d", got, initialFree-1)
	}

	if err := session.rm([]string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := len(disk.FreeBlocks); got != initialFree {
		t.Fatalf("free blocks after remove = %d, want %d", got, initialFree)
	}
}

func TestWriteFailsWithoutChangingExistingFileWhenNoBlocksAvailable(t *testing.T) {
	disk := newDisk()
	disk.BlockSize = 4
	disk.TotalBlocks = 1
	disk.FreeBlocks = []int{0}
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
		reader:      bufio.NewReader(bytes.NewBuffer(nil)),
		out:         &bytes.Buffer{},
	}

	if err := session.write([]string{"a.txt", "data"}); err != nil {
		t.Fatal(err)
	}
	file, err := session.resolve("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.write([]string{"a.txt", "datax"}); err == nil {
		t.Fatal("write succeeded, want allocation failure")
	}
	if file.Content != "data" {
		t.Fatalf("content changed after failed write: %q", file.Content)
	}
	if got := len(file.BlockIDs); got != 1 {
		t.Fatalf("file blocks after failed write = %d, want 1", got)
	}
	if got := len(disk.FreeBlocks); got != 0 {
		t.Fatalf("free blocks after failed write = %d, want 0", got)
	}
}

func TestMigrateDiskReallocatesBlocks(t *testing.T) {
	disk := newDiskWithConfig(4, 8)
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
		reader:      bufio.NewReader(bytes.NewBuffer(nil)),
		out:         &bytes.Buffer{},
	}

	if err := session.write([]string{"a.txt", "123456"}); err != nil {
		t.Fatal(err)
	}
	migrated, err := migrateDisk(disk, 3, 8)
	if err != nil {
		t.Fatal(err)
	}
	file := migrated.Nodes[disk.Nodes[disk.RootID].Children["a.txt"]]
	if got := len(file.BlockIDs); got != 2 {
		t.Fatalf("migrated blocks = %d, want 2", got)
	}
	if migrated.BlockSize != 3 || migrated.TotalBlocks != 8 {
		t.Fatalf("unexpected config: block_size=%d total_blocks=%d", migrated.BlockSize, migrated.TotalBlocks)
	}
	if got := len(migrated.FreeBlocks); got != 6 {
		t.Fatalf("free blocks after migration = %d, want 6", got)
	}
}

func TestMigrateDiskFailsWhenNewCapacityIsTooSmall(t *testing.T) {
	disk := newDiskWithConfig(4, 8)
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
		reader:      bufio.NewReader(bytes.NewBuffer(nil)),
		out:         &bytes.Buffer{},
	}

	if err := session.write([]string{"a.txt", "123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateDisk(disk, 3, 1); err == nil {
		t.Fatal("migration succeeded, want capacity failure")
	}
	file := disk.Nodes[disk.Nodes[disk.RootID].Children["a.txt"]]
	if got := len(file.BlockIDs); got != 2 {
		t.Fatalf("original disk changed after failed migration: blocks=%d, want 2", got)
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
