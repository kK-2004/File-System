package pfs

import (
	"bufio"
	"bytes"
	"testing"
)

func TestPasswdUpdatesCurrentUserPassword(t *testing.T) {
	disk := newDisk()
	session := &Session{
		disk:        disk,
		currentID:   disk.RootID,
		currentUser: disk.Users[0],
		reader:      bufio.NewReader(bytes.NewBufferString("root\npassword123\npassword123\n")),
		out:         &bytes.Buffer{},
	}

	if err := session.passwd(nil); err != nil {
		t.Fatalf("passwd returned error: %v", err)
	}

	if got, want := disk.Users[0].PasswordHash, hashPassword("password123"); got != want {
		t.Fatalf("password hash = %q, want %q", got, want)
	}
}
