package pfs

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

var errLoginExit = errors.New("login cancelled")
var errLoginReenterUsername = errors.New("re-enter username")

func (s *Session) loop() {
	for {
		fmt.Fprintf(s.out, "%s@FileSystem %s > ", s.currentUser.Name, s.pwd())
		line, err := s.reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(s.out)
			return
		}
		if err != nil {
			fmt.Fprintln(s.out, err)
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "exit" {
			_ = s.persist()
			return
		}
		if err := s.exec(fields[0], fields[1:]); err != nil {
			fmt.Fprintln(s.out, err)
		}
		_ = s.persist()
	}
}

func (s *Session) exec(cmd string, args []string) error {
	switch cmd {
	case "main", "help":
		s.help()
	case "useradd":
		return s.useradd(args)
	case "su":
		return s.su(args)
	case "passwd":
		return s.passwd(args)
	case "pwd":
		fmt.Fprintln(s.out, s.pwd())
	case "clear", "cls":
		clearScreen()
	case "mkdir":
		return s.mkdir(args)
	case "cd":
		return s.cd(args)
	case "touch":
		return s.touch(args)
	case "vim", "write":
		return s.write(args)
	case "more", "cat":
		return s.more(args)
	case "cp":
		return s.cp(args)
	case "mv":
		return s.mv(args)
	case "rename":
		return s.rename(args)
	case "tree":
		return s.tree(args)
	case "ls":
		return s.ls(args)
	case "ll":
		return s.ll(args)
	case "stat":
		return s.stat(args)
	case "detail":
		s.detail()
	case "rm":
		return s.rm(args)
	default:
		return fmt.Errorf("command not supported: %s", cmd)
	}
	return nil
}

func (s *Session) help() {
	fmt.Fprintln(s.out, `Supported commands:
  useradd [name]          add a user (root only)
  su username            switch user
  passwd [username]      change password
  pwd                    print current path
  clear | cls            clear the terminal
  mkdir name             create a directory
  touch name             create a file
  vim name [content...]  write file content; without content, finish with a single dot
  more name              print file content
  cd path                change directory; supports /, .., ~
  cp src target_dir      copy a file
  mv src target_dir      move a file or directory
  rename old new         rename an entry in the current directory
  tree [-d depth]        print full tree, or limit depth with -d
  ls                     list directory entries
  ll                     list detailed directory entries
  stat name              show file or directory metadata
  detail                 show disk summary
  rm [-r] name           remove a file or directory
  exit                   save and quit`)
}

func (s *Session) useradd(args []string) error {
	if s.currentUser.ID != rootUserID {
		return errors.New("only root can add users")
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	for name == "" {
		fmt.Fprint(s.out, "username: ")
		text, _ := s.reader.ReadString('\n')
		name = strings.TrimSpace(text)
	}
	if strings.Contains(name, "/") || name == "." || name == ".." {
		return errors.New("invalid username")
	}
	if _, ok := s.findUser(name); ok {
		return errors.New("username already exists")
	}
	password, err := s.readPasswordTwice()
	if err != nil {
		return err
	}

	user := User{ID: s.disk.NextUserID, Name: name, PasswordHash: hashPassword(password)}
	s.disk.NextUserID++
	s.disk.Users = append(s.disk.Users, user)

	home, err := s.mustDir("/home")
	if err != nil {
		return err
	}
	dir := s.disk.newNode(name, nodeDir, home.ID, user.ID)
	home.Children[name] = dir.ID
	home.Modified = now()
	fmt.Fprintf(s.out, "created user %s\n", name)
	return nil
}

func (s *Session) su(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: su username")
	}
	return s.login(args[0])
}

func (s *Session) passwd(args []string) error {
	targetName := s.currentUser.Name
	if len(args) > 1 {
		return errors.New("usage: passwd [username]")
	}
	if len(args) == 1 {
		if s.currentUser.ID != rootUserID {
			return errors.New("only root can change another user's password")
		}
		targetName = args[0]
	}

	index := s.findUserIndex(targetName)
	if index == -1 {
		return errors.New("user not found")
	}

	target := s.disk.Users[index]
	requiresCurrentPassword := s.currentUser.ID != rootUserID || target.ID == s.currentUser.ID
	if requiresCurrentPassword {
		fmt.Fprint(s.out, "current password: ")
		currentPassword, err := s.readSecret()
		if err != nil {
			return err
		}
		if target.PasswordHash != hashPassword(currentPassword) {
			return errors.New("current password incorrect")
		}
	}

	newPassword, err := s.readPasswordTwice()
	if err != nil {
		return err
	}
	s.disk.Users[index].PasswordHash = hashPassword(newPassword)
	fmt.Fprintf(s.out, "password updated for %s\n", targetName)
	return nil
}

func (s *Session) login(username string) error {
	if username != "" {
		return s.loginOnce(username)
	}

	for {
		fmt.Fprint(s.out, "username: ")
		text, err := s.reader.ReadString('\n')
		if err != nil {
			return err
		}
		inputName := strings.TrimSpace(text)
		if inputName == "" {
			continue
		}
		if err := s.loginInteractive(inputName); err != nil {
			if errors.Is(err, errLoginExit) {
				return err
			}
			if errors.Is(err, errLoginReenterUsername) {
				continue
			}
			fmt.Fprintln(s.out, err)
			continue
		}
		return nil
	}
}

func (s *Session) loginInteractive(username string) error {
	user, ok := s.findUser(username)
	if !ok {
		fmt.Fprintln(s.out, "user not found")
		choice, err := s.readUsernameChoice()
		if err != nil {
			return err
		}
		if choice == "e" {
			return errLoginExit
		}
		return errLoginReenterUsername
	}

	for {
		fmt.Fprint(s.out, "password: ")
		password, err := s.readSecret()
		if err != nil {
			return err
		}
		if user.PasswordHash == hashPassword(password) {
			return s.finishLogin(user)
		}

		fmt.Fprintln(s.out, "password incorrect")
		choice, err := s.readLoginChoice()
		if err != nil {
			return err
		}
		switch choice {
		case "r":
			continue
		case "u":
			return errLoginReenterUsername
		case "e":
			return errLoginExit
		}
	}
}

func (s *Session) loginOnce(username string) error {
	user, ok := s.findUser(username)
	if !ok {
		return errors.New("user not found")
	}
	if s.currentUser.ID != rootUserID || s.currentUser.Name == "" {
		for {
			fmt.Fprint(s.out, "password: ")
			password, err := s.readSecret()
			if err != nil {
				return err
			}
			if user.PasswordHash == hashPassword(password) {
				break
			}
			if s.currentUser.Name != "" {
				return errors.New("password incorrect")
			}
			fmt.Fprintln(s.out, "password incorrect")
		}
	}
	return s.finishLogin(user)
}

func (s *Session) finishLogin(user User) error {
	s.currentUser = user
	target := "/root"
	if user.ID != rootUserID {
		target = path.Join("/home", user.Name)
	}
	node, err := s.mustDir(target)
	if err != nil {
		s.currentID = s.disk.RootID
		return nil
	}
	s.currentID = node.ID
	return nil
}

func (s *Session) readLoginChoice() (string, error) {
	for {
		fmt.Fprint(s.out, "choose: [r]etry password, [u]sername, [e]xit: ")
		line, err := s.readSecret()
		if err != nil {
			return "", err
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "r", "u", "e":
			return choice, nil
		default:
			fmt.Fprintln(s.out, "invalid choice")
		}
	}
}

func (s *Session) readUsernameChoice() (string, error) {
	for {
		fmt.Fprint(s.out, "choose: [u]sername, [e]xit: ")
		line, err := s.readSecret()
		if err != nil {
			return "", err
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "u", "e":
			return choice, nil
		default:
			fmt.Fprintln(s.out, "invalid choice")
		}
	}
}

func (s *Session) findUser(name string) (User, bool) {
	for _, user := range s.disk.Users {
		if user.Name == name {
			return user, true
		}
	}
	return User{}, false
}

func (s *Session) findUserIndex(name string) int {
	for i, user := range s.disk.Users {
		if user.Name == name {
			return i
		}
	}
	return -1
}

func (s *Session) readPasswordTwice() (string, error) {
	for attempts := 0; attempts < 3; attempts++ {
		fmt.Fprint(s.out, "password: ")
		first, err := s.readSecret()
		if err != nil {
			return "", err
		}
		fmt.Fprint(s.out, "confirm: ")
		second, err := s.readSecret()
		if err != nil {
			return "", err
		}
		if first == second {
			return first, nil
		}
		fmt.Fprintln(s.out, "passwords do not match")
	}
	return "", errors.New("too many failed attempts")
}

func (s *Session) readSecret() (string, error) {
	line, err := s.reader.ReadString('\n')
	return strings.TrimSpace(line), err
}

func (s *Session) persist() error {
	return saveDisk(s.diskPath, s.disk)
}
