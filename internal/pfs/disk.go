package pfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

func newDisk() *Disk {
	return newDiskWithConfig(defaultBlockSize, defaultTotalBlocks)
}

func newDiskWithConfig(blockSize, totalBlocks int) *Disk {
	now := time.Now()
	disk := &Disk{
		FormatVersion: diskFormat,
		NextNodeID:    1,
		NextUserID:    1,
		BlockSize:     blockSize,
		TotalBlocks:   totalBlocks,
		Nodes:         map[string]*Node{},
		Users: []User{
			{ID: rootUserID, Name: "root", PasswordHash: hashPassword("root")},
		},
		Created: now,
		Updated: now,
	}
	disk.FreeBlocks = make([]int, disk.TotalBlocks)
	for i := range disk.FreeBlocks {
		disk.FreeBlocks[i] = i
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
	if d.BlockSize <= 0 {
		d.BlockSize = defaultBlockSize
	}
	if d.TotalBlocks <= 0 {
		d.TotalBlocks = defaultTotalBlocks
	}

	for _, node := range d.Nodes {
		if node.Type == nodeDir && node.Children == nil {
			node.Children = map[string]string{}
		}
		if node.Type != nodeFile {
			node.BlockIDs = nil
			continue
		}
		needed := d.blocksForBytes(len([]byte(node.Content)))
		if needed == 0 {
			node.BlockIDs = nil
			continue
		}
		if len(node.BlockIDs) != needed || !d.validBlockIDs(node.BlockIDs) {
			node.BlockIDs = nil
		}
	}
	d.rebuildFreeBlocks()
	for _, node := range d.Nodes {
		if node.Type != nodeFile || len(node.Content) == 0 || len(node.BlockIDs) > 0 {
			continue
		}
		blocks, err := d.allocateBlocks(d.blocksForBytes(len([]byte(node.Content))))
		if err != nil {
			continue
		}
		node.BlockIDs = blocks
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

func validateDiskConfig(blockSize, totalBlocks int) error {
	if blockSize <= 0 {
		return fmt.Errorf("block size must be greater than 0")
	}
	if totalBlocks <= 0 {
		return fmt.Errorf("total blocks must be greater than 0")
	}
	return nil
}

func migrateDisk(disk *Disk, blockSize, totalBlocks int) (*Disk, error) {
	if err := validateDiskConfig(blockSize, totalBlocks); err != nil {
		return nil, err
	}
	usedBytes := disk.fileContentBytes()
	capacity := blockSize * totalBlocks
	if capacity < usedBytes {
		return nil, fmt.Errorf("new disk capacity is too small: need at least %d bytes, got %d bytes", usedBytes, capacity)
	}

	data, err := json.Marshal(disk)
	if err != nil {
		return nil, err
	}
	var migrated Disk
	if err := json.Unmarshal(data, &migrated); err != nil {
		return nil, err
	}
	migrated.BlockSize = blockSize
	migrated.TotalBlocks = totalBlocks
	migrated.FreeBlocks = make([]int, totalBlocks)
	for i := range migrated.FreeBlocks {
		migrated.FreeBlocks[i] = i
	}

	files := migrated.sortedFiles()
	for _, node := range files {
		node.BlockIDs = nil
		blocks, err := migrated.allocateBlocks(migrated.blocksForBytes(len([]byte(node.Content))))
		if err != nil {
			return nil, err
		}
		node.BlockIDs = blocks
	}
	migrated.Updated = time.Now()
	return &migrated, nil
}

func replaceDiskFile(path string, disk *Disk) error {
	tmpPath := path + ".new"
	if err := saveDisk(tmpPath, disk); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func ensureDiskWithConfig(path string, blockSize, totalBlocks int) (*Disk, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if blockSize == 0 {
			blockSize = defaultBlockSize
		}
		if totalBlocks == 0 {
			totalBlocks = defaultTotalBlocks
		}
		if err := validateDiskConfig(blockSize, totalBlocks); err != nil {
			return nil, err
		}
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
		}
		disk := newDiskWithConfig(blockSize, totalBlocks)
		if err := saveDisk(path, disk); err != nil {
			return nil, err
		}
		fmt.Printf("disk not found, initialized %s\n", path)
		return disk, nil
	}

	disk, err := loadDisk(path)
	if err != nil {
		return nil, err
	}
	if blockSize == 0 {
		blockSize = disk.BlockSize
	}
	if totalBlocks == 0 {
		totalBlocks = disk.TotalBlocks
	}
	if err := validateDiskConfig(blockSize, totalBlocks); err != nil {
		return nil, err
	}
	if disk.BlockSize == blockSize && disk.TotalBlocks == totalBlocks {
		return disk, nil
	}
	migrated, err := migrateDisk(disk, blockSize, totalBlocks)
	if err != nil {
		return nil, err
	}
	if err := replaceDiskFile(path, migrated); err != nil {
		return nil, err
	}
	fmt.Printf("migrated %s to block_size=%d total_blocks=%d\n", path, blockSize, totalBlocks)
	return migrated, nil
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

func (n *Node) blocks() int {
	return len(n.BlockIDs)
}

func (d *Disk) blocksForBytes(size int) int {
	if size <= 0 {
		return 0
	}
	return (size + d.BlockSize - 1) / d.BlockSize
}

func (d *Disk) allocateBlocks(count int) ([]int, error) {
	if count <= 0 {
		return nil, nil
	}
	if len(d.FreeBlocks) < count {
		return nil, fmt.Errorf("not enough free disk blocks: need %d, available %d", count, len(d.FreeBlocks))
	}
	blocks := append([]int(nil), d.FreeBlocks[:count]...)
	d.FreeBlocks = append([]int(nil), d.FreeBlocks[count:]...)
	return blocks, nil
}

func (d *Disk) releaseBlocks(blocks []int) {
	if len(blocks) == 0 {
		return
	}
	free := make(map[int]bool, len(d.FreeBlocks)+len(blocks))
	for _, id := range d.FreeBlocks {
		if id >= 0 && id < d.TotalBlocks {
			free[id] = true
		}
	}
	for _, id := range blocks {
		if id >= 0 && id < d.TotalBlocks {
			free[id] = true
		}
	}
	d.FreeBlocks = d.sortedBlockIDs(free)
}

func (d *Disk) assignContent(node *Node, content string) error {
	needed := d.blocksForBytes(len([]byte(content)))
	availableSet := make(map[int]bool, len(d.FreeBlocks)+len(node.BlockIDs))
	for _, id := range d.FreeBlocks {
		if id >= 0 && id < d.TotalBlocks {
			availableSet[id] = true
		}
	}
	for _, id := range node.BlockIDs {
		if id >= 0 && id < d.TotalBlocks {
			availableSet[id] = true
		}
	}
	available := d.sortedBlockIDs(availableSet)
	if len(available) < needed {
		return fmt.Errorf("not enough free disk blocks: need %d, available %d", needed, len(available))
	}

	newBlocks := append([]int(nil), available[:needed]...)
	usedNew := make(map[int]bool, len(newBlocks))
	for _, id := range newBlocks {
		usedNew[id] = true
	}
	freeSet := map[int]bool{}
	for _, id := range available[needed:] {
		if !usedNew[id] {
			freeSet[id] = true
		}
	}
	node.Content = content
	node.BlockIDs = newBlocks
	d.FreeBlocks = d.sortedBlockIDs(freeSet)
	markChanged(node)
	return nil
}

func (d *Disk) validBlockIDs(blocks []int) bool {
	seen := map[int]bool{}
	for _, id := range blocks {
		if id < 0 || id >= d.TotalBlocks || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (d *Disk) rebuildFreeBlocks() {
	used := map[int]bool{}
	for _, node := range d.Nodes {
		if node.Type != nodeFile {
			continue
		}
		for _, id := range node.BlockIDs {
			if id >= 0 && id < d.TotalBlocks && !used[id] {
				used[id] = true
			}
		}
	}
	free := map[int]bool{}
	for i := 0; i < d.TotalBlocks; i++ {
		if !used[i] {
			free[i] = true
		}
	}
	d.FreeBlocks = d.sortedBlockIDs(free)
}

func (d *Disk) sortedBlockIDs(set map[int]bool) []int {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (d *Disk) sortedFiles() []*Node {
	files := make([]*Node, 0)
	for _, node := range d.Nodes {
		if node.Type == nodeFile {
			files = append(files, node)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		left, _ := strconv.Atoi(files[i].ID)
		right, _ := strconv.Atoi(files[j].ID)
		return left < right
	})
	return files
}

func (d *Disk) fileContentBytes() int {
	total := 0
	for _, node := range d.Nodes {
		if node.Type == nodeFile {
			total += len([]byte(node.Content))
		}
	}
	return total
}
