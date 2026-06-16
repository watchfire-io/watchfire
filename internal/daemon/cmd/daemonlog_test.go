package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatingFileWriter_UnderCapAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	w, err := newRotatingFileWriter(path, 4096, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.f.Close()

	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = 'x'
	}
	n, err := w.Write(payload)
	if err != nil || n != 1024 {
		t.Fatalf("write returned (%d, %v); want (1024, nil)", n, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat active: %v", err)
	}
	if info.Size() != 1024 {
		t.Errorf("active file size = %d; want 1024", info.Size())
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("daemon.log.1 should not exist after under-cap write; stat err = %v", err)
	}
}

func TestRotatingFileWriter_CapCrossingRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	w, err := newRotatingFileWriter(path, 1024, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.f.Close()

	first := make([]byte, 1000)
	for i := range first {
		first[i] = 'a'
	}
	if _, err := w.Write(first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	second := make([]byte, 100)
	for i := range second {
		second[i] = 'b'
	}
	if _, err := w.Write(second); err != nil {
		t.Fatalf("second write: %v", err)
	}

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if len(backup) != 1000 || backup[0] != 'a' {
		t.Errorf("backup len=%d head=%q; want 1000 bytes of 'a'", len(backup), backup[:min(8, len(backup))])
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if len(active) != 100 || active[0] != 'b' {
		t.Errorf("active len=%d head=%q; want 100 bytes of 'b'", len(active), active[:min(8, len(active))])
	}
	if w.size != 100 {
		t.Errorf("w.size = %d; want 100", w.size)
	}
}

func TestRotatingFileWriter_ExistingOversizedFileRotatesOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	// Pre-populate an oversized file (simulating upgrade from a build
	// without the cap).
	pre := make([]byte, 2048)
	for i := range pre {
		pre[i] = 'z'
	}
	if err := os.WriteFile(path, pre, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, err := newRotatingFileWriter(path, 1024, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.f.Close()

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if len(backup) != 2048 {
		t.Errorf("backup len = %d; want 2048 (pre-populated content)", len(backup))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fresh active: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("fresh active size = %d; want 0", info.Size())
	}
	if w.size != 0 {
		t.Errorf("w.size after startup rotate = %d; want 0", w.size)
	}
}

func TestRotatingFileWriter_MultipleRotationsKeepOnlyOneBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	w, err := newRotatingFileWriter(path, 512, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.f.Close()

	// Force three rotations by writing past the cap repeatedly.
	chunk := make([]byte, 400)
	for i := range chunk {
		chunk[i] = 'r'
	}
	for i := 0; i < 6; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("daemon.log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("daemon.log.1 missing: %v", err)
	}
	for _, ext := range []string{".2", ".3", ".4"} {
		if _, err := os.Stat(path + ext); !os.IsNotExist(err) {
			t.Errorf("daemon.log%s should not exist; stat err = %v", ext, err)
		}
	}
}

func TestRotatingFileWriter_ConcurrentWritesDoNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	const fileCap int64 = 5 * 1024
	w, err := newRotatingFileWriter(path, fileCap, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.f.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("goroutine %02d: %s\n", id, string(make([]byte, 80))))
			if _, err := w.Write(payload); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent write error: %v", err)
	}

	activeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat active: %v", err)
	}
	total := activeInfo.Size()
	if backupInfo, err := os.Stat(path + ".1"); err == nil {
		total += backupInfo.Size()
	}
	if total > 2*fileCap {
		t.Errorf("total bytes on disk = %d; want <= %d", total, 2*fileCap)
	}
}

func TestReclaimOversizedDaemonLogs_TruncatesActiveAndDropsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	// Oversized active file: 3840 bytes of bulk + a 256-byte recognizable tail.
	bulk := make([]byte, 3840)
	for i := range bulk {
		bulk[i] = 'A'
	}
	tail := make([]byte, 256)
	for i := range tail {
		tail[i] = 'B'
	}
	if err := os.WriteFile(path, append(bulk, tail...), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}
	// Oversized legacy backup that should be dropped outright.
	if err := os.WriteFile(path+".1", make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	reclaimOversizedDaemonLogs(path, 1024 /*fileCap*/, 256 /*tailBytes*/)

	// Backup gone.
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("daemon.log.1 should have been removed; stat err = %v", err)
	}
	// Active reclaimed: smaller than original, still under-ish, keeps the tail, drops the bulk.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if int64(len(content)) >= 4096 {
		t.Errorf("active not reclaimed: size = %d, want < 4096", len(content))
	}
	if !strings.Contains(string(content), "migrated on startup") {
		t.Errorf("active missing migration marker: %q", firstBytes(content, 120))
	}
	if !strings.Contains(string(content), strings.Repeat("B", 256)) {
		t.Errorf("active should retain the 256-byte tail")
	}
	if strings.Contains(string(content), strings.Repeat("A", 3840)) {
		t.Errorf("active should have dropped the leading bulk")
	}
}

func TestReclaimOversizedDaemonLogs_LeavesNormalFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	active := []byte("a normal, under-cap active log line\n")
	backup := []byte("a normal, under-cap backup\n")
	if err := os.WriteFile(path, active, 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}
	if err := os.WriteFile(path+".1", backup, 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	reclaimOversizedDaemonLogs(path, 1024 /*fileCap*/, 256 /*tailBytes*/)

	if got, _ := os.ReadFile(path); string(got) != string(active) {
		t.Errorf("under-cap active file was modified: %q", string(got))
	}
	if got, _ := os.ReadFile(path + ".1"); string(got) != string(backup) {
		t.Errorf("under-cap backup file was modified: %q", string(got))
	}
}

func firstBytes(b []byte, n int) string {
	if len(b) < n {
		return string(b)
	}
	return string(b[:n])
}
