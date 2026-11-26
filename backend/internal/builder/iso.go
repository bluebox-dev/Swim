package builder

// Windows installation ISOs as install images. An ISO placed in the ISO folder (over
// ssh — there is no upload in the web UI) can be inspected and one of its editions
// turned into a normal SWIM image:
//
//	iso_inspect {iso}                    → result.editions: what sources/install.wim|esd holds
//	iso_import  {iso, index, version?}   → exports that edition to <storage>/images/<channel>/<version>.wim
//
// The image lands in "pending" like a capture and is published by approving it. Stock
// install media is already generalized and not CompactOS-compressed; the idle inspector
// (inspect.go) verifies both from the WIM and records checks S-01/S-02, which is what
// makes the image installable. Each edition gets its own channel ("iso-<edition>"), so
// a published ISO image does not replace the image captured from the golden master.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/ids"
	"swim/backend/internal/vm"
)

// Edition is one image inside an ISO's install.wim / install.esd.
type Edition struct {
	Index         int    `json:"index"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	EditionID     string `json:"edition_id,omitempty"`
	Arch          string `json:"arch,omitempty"`
	Version       string `json:"version,omitempty"` // e.g. 10.0.26100.1742
	Languages     string `json:"languages,omitempty"`
	ExpandedBytes int64  `json:"expanded_bytes"`
}

func (w *Worker) isoPath(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || !strings.HasSuffix(strings.ToLower(name), ".iso") {
		return "", fmt.Errorf("invalid ISO name %q", name)
	}
	p := filepath.Join(w.ISODir, name)
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", fmt.Errorf("%s is not in the ISO folder", name)
	}
	return p, nil
}

// withISO loop-mounts the ISO read-only and calls fn with the path of its
// sources/install.wim (or install.esd).
func withISO(ctx context.Context, iso string, fn func(wim string) error) error {
	mnt, err := os.MkdirTemp("", "swim-iso-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	var last []byte
	mounted := false
	for _, fs := range []string{"udf", "iso9660"} { // Windows media is UDF; small test media may be ISO 9660
		if last, err = exec.CommandContext(ctx, "mount", "-o", "loop,ro", "-t", fs, iso, mnt).CombinedOutput(); err == nil {
			mounted = true
			break
		}
	}
	if !mounted {
		return fmt.Errorf("mount %s: %v: %s", filepath.Base(iso), err, strings.TrimSpace(string(last)))
	}
	defer func() { _ = exec.Command("umount", "-l", mnt).Run() }()
	// file names on the media are case-insensitive for Windows, not for us
	var wim string
	_ = filepath.Walk(mnt, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(mnt, p)
		switch l := strings.ToLower(filepath.ToSlash(rel)); {
		case fi.IsDir() && l != "." && l != "sources":
			return filepath.SkipDir
		case l == "sources/install.wim", l == "sources/install.esd" && wim == "":
			wim = p
		}
		return nil
	})
	if wim == "" {
		return errors.New("no sources/install.wim or install.esd on this ISO — it is not Windows installation media")
	}
	return fn(wim)
}

var spBuildRe = regexp.MustCompile(`^\d+$`)

// wimEditions parses `wimlib-imagex info`.
func wimEditions(ctx context.Context, wim string) ([]Edition, error) {
	out, err := exec.CommandContext(ctx, "wimlib-imagex", "info", wim).Output()
	if err != nil {
		return nil, fmt.Errorf("wimlib-imagex info: %v", err)
	}
	var eds []Edition
	var cur *Edition
	var major, minor, build, sp string
	var total, links int64
	flush := func() {
		if cur == nil {
			return
		}
		if major != "" && build != "" {
			cur.Version = major + "." + minor + "." + build
			if spBuildRe.MatchString(sp) {
				cur.Version += "." + sp
			}
		}
		cur.ExpandedBytes = total
		if links > 0 && links < total {
			cur.ExpandedBytes = total - links
		}
		eds = append(eds, *cur)
		cur, major, minor, build, sp, total, links = nil, "", "", "", "", 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "Index" {
			flush()
			n, _ := strconv.Atoi(v)
			cur = &Edition{Index: n}
			continue
		}
		if cur == nil {
			continue
		}
		switch k {
		case "Name":
			cur.Name = v
		case "Description":
			cur.Description = v
		case "Edition ID":
			cur.EditionID = v
		case "Architecture":
			cur.Arch = v
		case "Languages":
			cur.Languages = v
		case "Major Version":
			major = v
		case "Minor Version":
			minor = v
		case "Build":
			build = v
		case "Service Pack Build":
			sp = v
		case "Total Bytes":
			total, _ = strconv.ParseInt(v, 10, 64)
		case "Hard Link Bytes":
			links, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	flush()
	if len(eds) == 0 {
		return nil, errors.New("the install image on this ISO lists no editions")
	}
	return eds, nil
}

func (w *Worker) isoInspect(ctx context.Context, p map[string]string, logf func(string, ...any)) (map[string]any, error) {
	iso, err := w.isoPath(p["iso"])
	if err != nil {
		return nil, err
	}
	var eds []Edition
	var file string
	var size int64
	err = withISO(ctx, iso, func(wim string) error {
		file = "sources/" + strings.ToLower(filepath.Base(wim))
		if fi, err := os.Stat(wim); err == nil {
			size = fi.Size()
		}
		var err error
		eds, err = wimEditions(ctx, wim)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, e := range eds {
		logf("edition %d: %s (%s, %s, %.1f GB installed)", e.Index, e.Name, e.Version, e.Arch, float64(e.ExpandedBytes)/1e9)
	}
	return map[string]any{"iso": p["iso"], "file": file, "file_bytes": size, "editions": eds}, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func archName(a string) string {
	switch strings.ToLower(a) {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return strings.ToLower(a)
}

func (w *Worker) isoImport(ctx context.Context, p map[string]string, logf func(string, ...any), lf io.Writer) (map[string]any, error) {
	iso, err := w.isoPath(p["iso"])
	if err != nil {
		return nil, err
	}
	index, _ := strconv.Atoi(p["index"])
	var res map[string]any
	err = withISO(ctx, iso, func(wim string) error {
		w.progress(ctx, "preflight", 1, "reading the ISO")
		eds, err := wimEditions(ctx, wim)
		if err != nil {
			return err
		}
		var ed *Edition
		for i := range eds {
			if eds[i].Index == index {
				ed = &eds[i]
			}
		}
		if ed == nil {
			return fmt.Errorf("this ISO has no edition with index %d", index)
		}
		version := p["version"]
		if version == "" {
			version = ed.Version
		}
		if !vm.ValidName(version) {
			return fmt.Errorf("invalid version %q: letters, digits, . _ -", version)
		}
		// room for the exported WIM: at most the size of the whole install image, + spare
		src, err := os.Stat(wim)
		if err != nil {
			return err
		}
		if free, _, err := statFree(w.Storage); err == nil && free < src.Size()+minFreeAfter {
			return fmt.Errorf("not enough space on the server: about %.1f GB needed, %.1f GB free",
				float64(src.Size()+minFreeAfter)/1e9, float64(free)/1e9)
		}

		chName := "iso-" + slug(ed.Name)
		if chName == "iso-" {
			chName = "iso-" + slug(ed.EditionID)
		}
		var channelID string
		err = w.Pool.QueryRow(ctx, `SELECT id FROM channels WHERE name=$1`, chName).Scan(&channelID)
		if errors.Is(err, pgx.ErrNoRows) {
			channelID = ids.New("ch_")
			release := ed.Version
			if parts := strings.Split(ed.Version, "."); len(parts) >= 3 {
				release = parts[2]
			}
			edition := ed.EditionID
			if edition == "" {
				edition = ed.Name
			}
			_, err = w.Pool.Exec(ctx, `INSERT INTO channels(id, name, os, release, arch, edition, min_disk_gb) VALUES ($1,$2,'windows',$3,$4,$5,64)`,
				channelID, chName, release, archName(ed.Arch), edition)
			logf("created channel %s for %s", chName, ed.Name)
		}
		if err != nil {
			return fmt.Errorf("channel %s: %w", chName, err)
		}
		var exists bool
		if err := w.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM images WHERE channel_id=$1 AND version=$2 AND status <> 'deleted')`,
			channelID, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%s version %s was already imported — delete it on the Images page first, or give another version name", ed.Name, version)
		}

		imageID := ids.New("img_")
		// a deleted row may still hold (channel, version): reuse its slot
		if _, err := w.Pool.Exec(ctx, `DELETE FROM images WHERE channel_id=$1 AND version=$2 AND status='deleted'
			AND NOT EXISTS(SELECT 1 FROM deployment_jobs WHERE image_id=images.id OR fallback_image_id=images.id)`, channelID, version); err != nil {
			return err
		}
		if _, err := w.Pool.Exec(ctx, `INSERT INTO images(id, channel_id, version, status, built_by) VALUES ($1,$2,$3,'building',$4)`,
			imageID, channelID, version, p["actor"]); err != nil {
			return fmt.Errorf("register image: %w", err)
		}
		res = map[string]any{"image_id": imageID}
		rel := filepath.Join("images", chName, version+".wim")
		out := filepath.Join(w.Storage, rel)
		fail := func(step string, err error) error {
			w.event(ctx, imageID, step, "failed", err.Error())
			_, _ = w.Pool.Exec(context.Background(), `UPDATE images SET status='failed' WHERE id=$1`, imageID)
			_ = os.Remove(out)
			return err
		}
		if _, err := os.Stat(out); err == nil {
			return fail("prepare", fmt.Errorf("%s already exists", rel))
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return fail("prepare", err)
		}
		w.event(ctx, imageID, "preflight", "ok", fmt.Sprintf("%s · edition %d %s · %s · %.1f GB installed",
			p["iso"], ed.Index, ed.Name, ed.Version, float64(ed.ExpandedBytes)/1e9))

		// install.esd is LZMS solid-compressed, which WinPE's apply path handles poorly:
		// always write a plain LZX WIM. A .wim source is copied without recompressing.
		args := []string{"export", wim, strconv.Itoa(index), out, ed.Name, "Imported by SWIM from " + p["iso"], "--compress=LZX"}
		w.event(ctx, imageID, "capture", "running", "exporting "+ed.Name+" from "+p["iso"])
		logf("wimlib-imagex %s", strings.Join(args[:4], " "))
		start := time.Now()
		cmd := exec.CommandContext(ctx, "wimlib-imagex", args...)
		pr, pw := io.Pipe()
		cmd.Stdout, cmd.Stderr = pw, pw
		w.progress(ctx, "capture", 3, "exporting "+ed.Name)
		done := make(chan struct{})
		go func() {
			copyProgress(pr, lf, func(pct int) {
				w.progress(ctx, "capture", 3+float64(pct)*0.87, fmt.Sprintf("writing the WIM: %d%%", pct))
			})
			close(done)
		}()
		err = cmd.Run()
		pw.Close()
		<-done
		if err != nil {
			return fail("capture", fmt.Errorf("wimlib-imagex export: %v", err))
		}
		logf("export finished in %s", time.Since(start).Round(time.Second))

		w.progress(ctx, "hash", 91, "SHA-256 of the WIM")
		sum, n, err := hashFile(out)
		if err != nil {
			return fail("hash", err)
		}
		// wof_files stays NULL: the idle inspector reads the WIM and records S-01/S-02
		if _, err := w.Pool.Exec(ctx, `UPDATE images SET status='pending', file_path=$2, sha256=$3, size_bytes=$4, expanded_bytes=$5 WHERE id=$1`,
			imageID, filepath.ToSlash(rel), sum, n, ed.ExpandedBytes); err != nil {
			return fail("register", err)
		}
		w.event(ctx, imageID, "capture", "ok", fmt.Sprintf("%d bytes, sha256 %s", n, sum))
		logf("image %s → pending (%s %s, %.2f GB)", imageID, chName, version, float64(n)/1e9)
		res = map[string]any{"image_id": imageID, "channel": chName, "version": version, "file": rel, "sha256": sum,
			"size_bytes": n, "expanded_bytes": ed.ExpandedBytes, "edition": ed.Name}
		return nil
	})
	return res, err
}
