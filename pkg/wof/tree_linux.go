//go:build linux

package wof

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ntfs-3g's FUSE driver refuses all operations on WOF files without its
// system-compression plugin, so only the listing uses FUSE (read-only, xattrs work);
// reading and rewriting go through the ntfs-unwof helper (libntfs-3g, unmounted volume).

const reparseXattr = "system.ntfs_reparse_data"

// Entry is one WOF file: its volume path ("/Windows/…") and compression algorithm.
type Entry struct {
	Path string
	Algo int
}

// List walks a read-only ntfs-3g mount and returns the WOF files, one path per
// inode (hard links are rewritten once). Files with an unsupported algorithm (LZX)
// are returned too; Fix reports them as failed.
func List(mountRoot string) ([]Entry, error) { return listUnique(mountRoot, true) }

func listUnique(mountRoot string, dedupe bool) ([]Entry, error) {
	seen := map[uint64]bool{}
	var out []Entry
	err := filepath.WalkDir(mountRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		var b [64]byte
		n, err := unix.Lgetxattr(p, reparseXattr, b[:])
		if err != nil || n < 8 || binary.LittleEndian.Uint32(b[:]) != ReparseTag {
			return nil
		}
		algo, err := ParseReparse(b[:n])
		if err != nil {
			algo = -1
		}
		if fi, err := os.Lstat(p); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				if dedupe && seen[st.Ino] {
					return nil
				}
				seen[st.Ino] = true
			}
		}
		out = append(out, Entry{"/" + filepath.ToSlash(strings.TrimPrefix(p, strings.TrimSuffix(mountRoot, "/")+"/")), algo})
		return nil
	})
	return out, err
}

// Stats of a Fix run.
type Stats struct {
	Files     int64 // rewritten (or, in a dry run, verified)
	InBytes   int64 // compressed stream bytes
	OutBytes  int64 // uncompressed bytes
	PEChecked int64 // PE images whose checksum was verified
	PEBad     int64 // … and did not match (left compressed)
	Failed    int64
	// Left WOF-compressed (LZX, unverifiable PE checksum, …). Full Windows still reads
	// them through wof.sys; WinPE only needs the code it runs to be plain, see SystemLeft.
	FailedPaths []string
	// WOF files counted after the rewrite in \Windows\System32 and \Windows\Boot
	// (by walking those folders, so hard links listed under WinSxS are included); -1 = not counted.
	Critical int
}

// SystemLeft counts files still WOF-compressed where WinPE runs code from the new
// image (\Windows\System32 — DISM, drivers, bcdboot — and \Windows\Boot).
func (s Stats) SystemLeft() int {
	if s.Critical >= 0 {
		return s.Critical
	}
	n := 0
	for _, p := range s.FailedPaths {
		if critical(p) {
			n++
		}
	}
	return n
}

func underWindows(p string) bool { return strings.HasPrefix(strings.ToLower(p), "/windows/") }

func critical(p string) bool {
	l := strings.ToLower(p)
	return strings.HasPrefix(l, "/windows/system32/") || strings.HasPrefix(l, "/windows/boot/")
}

// Fix decompresses the given WOF files on an UNMOUNTED NTFS device in batches.
// For every file the data must decompress cleanly and PE images must match their
// checksum before anything is written; failures are left untouched and counted.
// With dryRun nothing is written.
func Fix(helper, device string, paths []Entry, tmp string, batch int, dryRun bool, logf func(string, ...any), progress ...func(done, total int)) (Stats, error) {
	var st Stats
	complaints := 0
	complain := func(f string, a ...any) {
		// always report problems with system files; app files only the first few
		if complaints++; complaints <= 20 || (len(a) > 0 && underWindows(fmt.Sprint(a[0]))) {
			logf(f, a...)
		}
	}
	for start := 0; start < len(paths); start += batch {
		end := min(start+batch, len(paths))
		dir, err := os.MkdirTemp(tmp, "wof-")
		if err != nil {
			return st, err
		}
		pairs, err := fixBatch(helper, device, paths[start:end], dir, &st, complain)
		if err == nil && !dryRun && len(pairs) > 0 {
			err = writeBatch(helper, device, pairs, &st, complain)
		}
		_ = os.RemoveAll(dir)
		if err != nil {
			return st, err
		}
		for _, f := range progress {
			f(end, len(paths))
		}
		if (start/batch)%10 == 0 || end == len(paths) {
			logf("WOF: %d/%d files, %.2f GB → %.2f GB, PE checked %d, failed %d",
				end, len(paths), float64(st.InBytes)/1e9, float64(st.OutBytes)/1e9, st.PEChecked, st.Failed)
		}
	}
	return st, nil
}

type pair struct{ path, data string }

func fixBatch(helper, device string, paths []Entry, dir string, st *Stats, complain func(string, ...any)) ([]pair, error) {
	var in bytes.Buffer
	for _, p := range paths {
		in.WriteString(p.Path)
		in.WriteByte(0)
	}
	cmd := exec.Command(helper, "read", device, dir)
	cmd.Stdin = &in
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("ntfs-unwof read: %v: %s", err, errb.String())
	}
	type item struct {
		i    int
		size int64
	}
	var items []item
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 3)
		if len(f) < 3 {
			continue
		}
		i, _ := strconv.Atoi(f[0])
		if f[1] == "ERR" {
			st.Failed++
			st.FailedPaths = append(st.FailedPaths, paths[i].Path)
			complain("WOF %s: %s", paths[i].Path, f[2])
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		items = append(items, item{i, size})
	}
	var (
		mu    sync.Mutex
		pairs []pair
		wg    sync.WaitGroup
		sem   = make(chan struct{}, 8)
	)
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it item) {
			defer func() { <-sem; wg.Done() }()
			p := paths[it.i].Path
			fail := func(f string, a ...any) {
				mu.Lock()
				st.Failed++
				st.FailedPaths = append(st.FailedPaths, p)
				complain("WOF %s: "+f, append([]any{p}, a...)...)
				mu.Unlock()
			}
			src := filepath.Join(dir, strconv.Itoa(it.i)+".wof")
			stream, err := os.ReadFile(src)
			if err != nil {
				fail("%v", err)
				return
			}
			_ = os.Remove(src)
			data, err := Decompress(stream, it.size, paths[it.i].Algo)
			if err != nil {
				fail("%v", err)
				return
			}
			stored, sum, isPE := PEChecksum(data)
			if isPE && stored != sum {
				// some resource-only images ship with stale checksums; never write data we
				// cannot verify — the file stays WOF-compressed (full Windows reads it)
				mu.Lock()
				st.PEBad++
				mu.Unlock()
				fail("PE checksum mismatch (stored %08x, computed %08x) — left compressed", stored, sum)
				return
			}
			dst := filepath.Join(dir, strconv.Itoa(it.i)+".bin")
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				fail("%v", err)
				return
			}
			mu.Lock()
			if isPE {
				st.PEChecked++
			}
			st.Files++
			st.InBytes += int64(len(stream))
			st.OutBytes += int64(len(data))
			pairs = append(pairs, pair{p, dst})
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	return pairs, nil
}

func writeBatch(helper, device string, pairs []pair, st *Stats, complain func(string, ...any)) error {
	var in bytes.Buffer
	for _, p := range pairs {
		in.WriteString(p.path)
		in.WriteByte(0)
		in.WriteString(p.data)
		in.WriteByte(0)
	}
	cmd := exec.Command(helper, "write", device)
	cmd.Stdin = &in
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	bad := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "ERR") {
			bad++
			if f := strings.SplitN(line, "\t", 3); len(f) > 1 {
				st.FailedPaths = append(st.FailedPaths, f[1])
			}
			complain("WOF write: %s", line)
		}
	}
	if bad > 0 {
		st.Failed += int64(bad)
		st.Files -= int64(bad)
	}
	if err != nil && bad == 0 {
		return fmt.Errorf("ntfs-unwof write: %v: %s", err, errb.String())
	}
	if strings.Contains(errb.String(), "ntfs_umount") {
		return fmt.Errorf("ntfs-unwof write: %s", errb.String())
	}
	return nil
}

// Uncompress lists the WOF files of an NTFS device (temporary read-only ntfs-3g
// mount), then rewrites them as plain files with the helper (dryRun: verify only).
// The device must not be mounted by anyone else.
func Uncompress(device, helper, tmp string, dryRun bool, logf func(string, ...any), progress ...func(done, total int)) (Stats, error) {
	mnt, err := os.MkdirTemp(tmp, "ntfs-ro-")
	if err != nil {
		return Stats{}, err
	}
	defer os.Remove(mnt)
	if b, err := exec.Command("mount", "-t", "ntfs-3g", "-o", "ro,streams_interface=windows", device, mnt).CombinedOutput(); err != nil {
		return Stats{}, fmt.Errorf("mount %s: %v: %s", device, err, b)
	}
	list, err := List(mnt)
	if b, uerr := exec.Command("umount", mnt).CombinedOutput(); uerr != nil {
		return Stats{}, fmt.Errorf("umount %s: %v: %s", mnt, uerr, b)
	}
	if err != nil {
		return Stats{}, err
	}
	logf("WOF: %d compressed files (one per inode) on %s", len(list), device)
	if len(list) == 0 {
		return Stats{}, nil
	}
	st, err := Fix(helper, device, list, tmp, 2000, dryRun, logf, progress...)
	st.Critical = -1
	if err != nil || dryRun {
		return st, err
	}
	// count what is really left where WinPE runs code (a fresh read-only mount)
	if b, err := exec.Command("mount", "-t", "ntfs-3g", "-o", "ro,streams_interface=windows", device, mnt).CombinedOutput(); err != nil {
		return st, fmt.Errorf("re-mount %s: %v: %s", device, err, b)
	}
	st.Critical = 0
	for _, sub := range []string{"Windows/System32", "Windows/Boot"} {
		left, lerr := listUnique(filepath.Join(mnt, sub), false)
		if lerr != nil {
			err = lerr
		}
		for _, e := range left {
			logf("WOF still compressed: /%s%s", sub, e.Path)
		}
		st.Critical += len(left)
	}
	if b, uerr := exec.Command("umount", mnt).CombinedOutput(); uerr != nil && err == nil {
		err = fmt.Errorf("umount %s: %v: %s", mnt, uerr, b)
	}
	logf("WOF: %d files left compressed in Windows\\System32 and Windows\\Boot", st.Critical)
	return st, err
}
