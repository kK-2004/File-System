package pfs

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed web/*
var webFiles embed.FS

var webAssetVersion = fmt.Sprintf("%d", time.Now().Unix())

var terminalCommands = []string{
	"help", "main", "pwd", "mkdir", "touch", "vim", "write", "more", "cat",
	"cd", "cp", "mv", "rename", "tree", "ls", "ll", "stat", "detail", "rm",
	"clear", "cls", "exit",
}

type webServer struct {
	mu       sync.Mutex
	disk     *Disk
	diskPath string
	sessions map[string]*Session
	clients  map[*websocket.Conn]bool
}

type wsEvent struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Pwd     string `json:"pwd,omitempty"`
	At      string `json:"at"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	SessionID string `json:"session_id"`
	Username  string `json:"username"`
	Pwd       string `json:"pwd"`
	Output    string `json:"output"`
}

type execRequest struct {
	SessionID string `json:"session_id"`
	Line      string `json:"line"`
}

type execResponse struct {
	Output  string          `json:"output"`
	Pwd     string          `json:"pwd"`
	Exit    bool            `json:"exit"`
	Edit    *editPayload    `json:"edit,omitempty"`
	UserAdd *userAddPayload `json:"useradd,omitempty"`
}

type completeRequest struct {
	SessionID string `json:"session_id"`
	Line      string `json:"line"`
	Cursor    int    `json:"cursor"`
}

type completeResponse struct {
	Replacement string   `json:"replacement"`
	Suggestions []string `json:"suggestions"`
}

type editPayload struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type saveFileRequest struct {
	SessionID string `json:"session_id"`
	Filename  string `json:"filename"`
	Content   string `json:"content"`
}

type userAddPayload struct {
	Username string `json:"username"`
}

type createUserRequest struct {
	SessionID string `json:"session_id"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

type diskUsageResponse struct {
	BlockSize    int         `json:"block_size"`
	TotalBlocks  int         `json:"total_blocks"`
	UsedBlocks   int         `json:"used_blocks"`
	FreeBlocks   int         `json:"free_blocks"`
	UsedPercent  float64     `json:"used_percent"`
	UsedBlockIDs []int       `json:"used_block_ids"`
	Files        []fileUsage `json:"files"`
}

type diskConfigRequest struct {
	SessionID   string `json:"session_id"`
	BlockSize   int    `json:"block_size"`
	TotalBlocks int    `json:"total_blocks"`
}

type fileUsage struct {
	Path     string `json:"path"`
	Size     int    `json:"size"`
	Blocks   int    `json:"blocks"`
	BlockIDs []int  `json:"block_ids"`
}

func runWeb(args []string) int {
	flags := flag.NewFlagSet("web", flag.ExitOnError)
	diskPath := flags.String("disk", defaultDiskPath(), "virtual disk file")
	addr := flags.String("addr", "127.0.0.1:8080", "web server address")
	blockSize := flags.Int("block-size", 0, "migrate disk to this block size in bytes")
	totalBlocks := flags.Int("total-blocks", 0, "migrate disk to this total block count")
	_ = flags.Parse(args)

	disk, err := ensureDiskWithConfig(*diskPath, *blockSize, *totalBlocks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to prepare disk: %v\n", err)
		return 1
	}

	server := &webServer{
		disk:     disk,
		diskPath: *diskPath,
		sessions: map[string]*Session{},
		clients:  map[*websocket.Conn]bool{},
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	server.routes(router)

	fmt.Printf("web terminal: http://%s\n", *addr)
	if err := router.Run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func (w *webServer) routes(router *gin.Engine) {
	router.POST("/api/login", w.handleLogin)
	router.POST("/api/exec", w.handleExec)
	router.POST("/api/complete", w.handleComplete)
	router.POST("/api/file/save", w.handleSaveFile)
	router.POST("/api/user/create", w.handleCreateUser)
	router.POST("/api/disk/usage", w.handleDiskUsage)
	router.POST("/api/disk/config", w.handleDiskConfig)
	router.GET("/ws/events", w.handleEvents)

	staticFS, _ := fs.Sub(webFiles, "web")
	router.NoRoute(func(c *gin.Context) {
		name := strings.TrimPrefix(c.Request.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(staticFS, name)
		if err != nil {
			name = "index.html"
			data, err = fs.ReadFile(staticFS, name)
			if err != nil {
				writeError(c, http.StatusInternalServerError, err)
				return
			}
		}
		if name == "index.html" {
			c.Header("Cache-Control", "no-store")
			data = []byte(strings.ReplaceAll(string(data), "{{ASSET_VERSION}}", webAssetVersion))
		} else {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		c.Data(http.StatusOK, contentType, data)
	})
}

func (w *webServer) handleLogin(c *gin.Context) {
	var input loginRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" {
		writeError(c, http.StatusBadRequest, errors.New("username is required"))
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	user, ok := findUserInDisk(w.disk, input.Username)
	if !ok || user.PasswordHash != hashPassword(input.Password) {
		writeError(c, http.StatusUnauthorized, errors.New("username or password incorrect"))
		return
	}

	session := &Session{
		disk:        w.disk,
		diskPath:    w.diskPath,
		currentUser: user,
		reader:      bufio.NewReader(bytes.NewBuffer(nil)),
		out:         &bytes.Buffer{},
	}
	if err := session.finishLogin(user); err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}

	id, err := newSessionID()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	w.sessions[id] = session
	c.JSON(http.StatusOK, loginResponse{
		SessionID: id,
		Username:  user.Name,
		Pwd:       session.pwd(),
		Output:    fmt.Sprintf("login as %s\n", user.Name),
	})
}

func (w *webServer) handleExec(c *gin.Context) {
	var input execRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	session, ok := w.sessions[input.SessionID]
	if !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}

	line := strings.TrimSpace(input.Line)
	if line == "" {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd()})
		return
	}
	if line == "exit" {
		delete(w.sessions, input.SessionID)
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Exit: true, Output: "session closed\n"})
		return
	}
	if line == "clear" || line == "cls" {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd()})
		return
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd()})
		return
	}
	if fields[0] == "useradd" && len(fields) == 2 {
		if session.currentUser.ID != rootUserID {
			c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: "only root can add users\n"})
			return
		}
		if err := validateNewUsername(w.disk, fields[1]); err != nil {
			c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
			return
		}
		c.JSON(http.StatusOK, execResponse{
			Pwd:     session.pwd(),
			UserAdd: &userAddPayload{Username: fields[1]},
		})
		return
	}
	if isWebUnsupportedCommand(fields[0]) {
		c.JSON(http.StatusOK, execResponse{
			Pwd:    session.pwd(),
			Output: "this command needs interactive input; use the CLI for passwd/su\n",
		})
		return
	}
	if (fields[0] == "vim" || fields[0] == "write") && len(fields) == 2 {
		edit, err := session.prepareWebEdit(fields[1])
		if err != nil {
			c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
			return
		}
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Edit: edit})
		return
	}

	out := &bytes.Buffer{}
	session.out = out
	session.reader = bufio.NewReader(bytes.NewBuffer(nil))
	err := session.exec(fields[0], fields[1:])
	if err != nil {
		fmt.Fprintln(out, err)
	} else if persistErr := session.persist(); persistErr != nil {
		fmt.Fprintln(out, persistErr)
	} else if isMutatingCommand(fields[0]) {
		w.broadcastLocked("disk_changed", strings.TrimSpace(line), session.pwd())
	}
	c.JSON(http.StatusOK, execResponse{
		Output: out.String(),
		Pwd:    session.pwd(),
	})
}

func (w *webServer) handleComplete(c *gin.Context) {
	var input completeRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	session, ok := w.sessions[input.SessionID]
	if !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}

	line := input.Line
	if input.Cursor >= 0 && input.Cursor <= len(line) {
		line = line[:input.Cursor]
	}
	c.JSON(http.StatusOK, session.completeLine(line))
}

func (w *webServer) handleSaveFile(c *gin.Context) {
	var input saveFileRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	session, ok := w.sessions[input.SessionID]
	if !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}

	file, err := session.fileOrCreate(input.Filename)
	if err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	if err := session.canWrite(file); err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	if err := session.disk.assignContent(file, input.Content); err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	if err := session.persist(); err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	w.broadcastLocked("disk_changed", fmt.Sprintf("written %s", input.Filename), session.pwd())
	c.JSON(http.StatusOK, execResponse{
		Pwd:    session.pwd(),
		Output: fmt.Sprintf("written %s (%d bytes)\n", input.Filename, len([]byte(input.Content))),
	})
}

func (w *webServer) handleCreateUser(c *gin.Context) {
	var input createUserRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	session, ok := w.sessions[input.SessionID]
	if !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}
	if err := session.createUser(input.Username, input.Password); err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	if err := session.persist(); err != nil {
		c.JSON(http.StatusOK, execResponse{Pwd: session.pwd(), Output: err.Error() + "\n"})
		return
	}
	w.broadcastLocked("user_changed", fmt.Sprintf("created user %s", input.Username), session.pwd())
	c.JSON(http.StatusOK, execResponse{
		Pwd:    session.pwd(),
		Output: fmt.Sprintf("created user %s\n", input.Username),
	})
}

func (w *webServer) handleDiskUsage(c *gin.Context) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.sessions[input.SessionID]; !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}
	usedBlocks := w.disk.TotalBlocks - len(w.disk.FreeBlocks)
	usedPercent := 0.0
	if w.disk.TotalBlocks > 0 {
		usedPercent = float64(usedBlocks) * 100 / float64(w.disk.TotalBlocks)
	}
	c.JSON(http.StatusOK, diskUsageResponse{
		BlockSize:    w.disk.BlockSize,
		TotalBlocks:  w.disk.TotalBlocks,
		UsedBlocks:   usedBlocks,
		FreeBlocks:   len(w.disk.FreeBlocks),
		UsedPercent:  usedPercent,
		UsedBlockIDs: usedBlockIDs(w.disk),
		Files:        collectFileUsage(w.disk),
	})
}

func (w *webServer) handleDiskConfig(c *gin.Context) {
	var input diskConfigRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	session, ok := w.sessions[input.SessionID]
	if !ok {
		writeError(c, http.StatusUnauthorized, errors.New("session expired"))
		return
	}
	if session.currentUser.ID != rootUserID {
		writeError(c, http.StatusForbidden, errors.New("only root can change disk config"))
		return
	}
	migrated, err := migrateDisk(w.disk, input.BlockSize, input.TotalBlocks)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	if err := replaceDiskFile(w.diskPath, migrated); err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	w.disk = migrated
	for _, active := range w.sessions {
		active.disk = migrated
	}
	w.broadcastLocked("disk_changed", "disk config migrated", session.pwd())
	c.JSON(http.StatusOK, gin.H{
		"message":      fmt.Sprintf("migrated disk to block_size=%d total_blocks=%d", migrated.BlockSize, migrated.TotalBlocks),
		"block_size":   migrated.BlockSize,
		"total_blocks": migrated.TotalBlocks,
	})
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (w *webServer) handleEvents(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	w.mu.Lock()
	w.clients[conn] = true
	_ = conn.WriteJSON(wsEvent{Type: "connected", At: time.Now().Format(time.RFC3339)})
	w.mu.Unlock()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			w.mu.Lock()
			delete(w.clients, conn)
			w.mu.Unlock()
			_ = conn.Close()
			return
		}
	}
}

func (w *webServer) broadcastLocked(eventType, message, pwd string) {
	if len(w.clients) == 0 {
		return
	}
	event := wsEvent{
		Type:    eventType,
		Message: message,
		Pwd:     pwd,
		At:      time.Now().Format(time.RFC3339),
	}
	for conn := range w.clients {
		if err := conn.WriteJSON(event); err != nil {
			delete(w.clients, conn)
			_ = conn.Close()
		}
	}
}

func (s *Session) prepareWebEdit(filename string) (*editPayload, error) {
	if strings.Contains(filename, "/") {
		node, err := s.resolve(filename)
		if err != nil {
			return nil, err
		}
		if node.Type != nodeFile {
			return nil, errors.New("not a file")
		}
		if err := s.canWrite(node); err != nil {
			return nil, err
		}
		return &editPayload{Filename: filename, Content: node.Content}, nil
	}

	if err := validateName(filename); err != nil {
		return nil, err
	}
	parent := s.cwd()
	if id, exists := parent.Children[filename]; exists {
		node := s.disk.Nodes[id]
		if node.Type != nodeFile {
			return nil, errors.New("not a file")
		}
		if err := s.canWrite(node); err != nil {
			return nil, err
		}
		return &editPayload{Filename: filename, Content: node.Content}, nil
	}
	if err := s.canWrite(parent); err != nil {
		return nil, err
	}
	return &editPayload{Filename: filename}, nil
}

func (s *Session) createUser(name, password string) error {
	if s.currentUser.ID != rootUserID {
		return errors.New("only root can add users")
	}
	if password == "" {
		return errors.New("password cannot be empty")
	}
	if err := validateNewUsername(s.disk, name); err != nil {
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
	return nil
}

func validateNewUsername(disk *Disk, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return errors.New("invalid username")
	}
	if _, ok := findUserInDisk(disk, name); ok {
		return errors.New("username already exists")
	}
	return nil
}

func collectFileUsage(disk *Disk) []fileUsage {
	files := []fileUsage{}
	var walk func(node *Node, currentPath string)
	walk = func(node *Node, currentPath string) {
		if node.Type == nodeFile {
			files = append(files, fileUsage{
				Path:     currentPath,
				Size:     node.size(),
				Blocks:   node.blocks(),
				BlockIDs: append([]int(nil), node.BlockIDs...),
			})
			return
		}
		names := make([]string, 0, len(node.Children))
		for name := range node.Children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := disk.Nodes[node.Children[name]]
			childPath := path.Join(currentPath, name)
			if currentPath == "/" {
				childPath = "/" + name
			}
			walk(child, childPath)
		}
	}
	if root := disk.Nodes[disk.RootID]; root != nil {
		walk(root, "/")
	}
	return files
}

func usedBlockIDs(disk *Disk) []int {
	used := map[int]bool{}
	for _, node := range disk.Nodes {
		if node.Type != nodeFile {
			continue
		}
		for _, id := range node.BlockIDs {
			used[id] = true
		}
	}
	ids := make([]int, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (s *Session) completeLine(line string) completeResponse {
	tokenStart := strings.LastIndexAny(line, " \t") + 1
	prefix := line[tokenStart:]
	before := strings.TrimSpace(line[:tokenStart])

	var candidates []string
	if before == "" {
		candidates = matchingStrings(terminalCommands, prefix)
	} else {
		candidates = s.pathCompletionCandidates(prefix)
	}
	sort.Strings(candidates)

	replacement := prefix
	if len(candidates) == 1 {
		replacement = candidates[0]
		if before == "" {
			replacement += " "
		}
	} else if common := commonPrefix(candidates); common != "" && common != prefix {
		replacement = common
	}
	return completeResponse{Replacement: replacement, Suggestions: candidates}
}

func (s *Session) pathCompletionCandidates(prefix string) []string {
	dirInput, namePrefix := path.Split(prefix)
	dirNode := s.cwd()
	if dirInput != "" {
		node, err := s.resolve(strings.TrimSuffix(dirInput, "/"))
		if err != nil || node.Type != nodeDir {
			return nil
		}
		dirNode = node
	}
	if err := s.canRead(dirNode); err != nil {
		return nil
	}

	candidates := []string{}
	for _, child := range s.sortedChildren(dirNode) {
		if !strings.HasPrefix(child.Name, namePrefix) {
			continue
		}
		value := dirInput + child.Name
		if child.Type == nodeDir {
			value += "/"
		}
		candidates = append(candidates, value)
	}
	return candidates
}

func matchingStrings(values []string, prefix string) []string {
	matches := []string{}
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			matches = append(matches, value)
		}
	}
	return matches
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func isWebUnsupportedCommand(cmd string) bool {
	switch cmd {
	case "passwd", "su":
		return true
	default:
		return false
	}
}

func isMutatingCommand(cmd string) bool {
	switch cmd {
	case "mkdir", "touch", "vim", "write", "cp", "mv", "rename", "rm", "useradd", "passwd":
		return true
	default:
		return false
	}
}

func findUserInDisk(disk *Disk, name string) (User, bool) {
	for _, user := range disk.Users {
		if user.Name == name {
			return user, true
		}
	}
	return User{}, false
}

func newSessionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func writeError(c *gin.Context, status int, err error) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": err.Error(),
	})
}
