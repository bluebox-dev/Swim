/*
 * ntfs-unwof — offline helper for the SWIM builder (links libntfs-3g directly).
 *
 * ntfs-3g's FUSE driver refuses every operation on WOF (CompactOS) files unless the
 * system-compression plugin is installed, so the builder reads and rewrites them
 * through the library instead. Decompression itself is done by the Go caller
 * (pkg/wof); this program only moves bytes in and out of the NTFS volume.
 *
 *   ntfs-unwof read  <device> <outdir>   stdin: NUL-separated paths
 *       for path #i writes <outdir>/<i>.wof (the WofCompressedData stream) and prints
 *       "<i>\t<unnamed $DATA size>\t<stream size>\n" (or "<i>\tERR\t<reason>\n")
 *   ntfs-unwof write <device>            stdin: NUL-separated "path\0datafile\0" pairs
 *       replaces the unnamed $DATA with the contents of datafile, removes the
 *       WofCompressedData stream and the reparse point, clears RECALL_ON_OPEN;
 *       prints "OK\t<path>\n" or "ERR\t<path>\t<reason>\n"
 *
 * Exit status is 0 only if every item succeeded. The device must not be mounted.
 */
/* libntfs-3g's public headers expect its autoconf results */
#define HAVE_SYS_TYPES_H 1
#define HAVE_SYS_STAT_H 1
#define HAVE_STDINT_H 1
#define HAVE_STDDEF_H 1
#define HAVE_STDARG_H 1
#define HAVE_STDIO_H 1
#define HAVE_STDLIB_H 1
#define HAVE_STRING_H 1
#define HAVE_ERRNO_H 1
#define HAVE_LIMITS_H 1
#define HAVE_GETTIMEOFDAY 1

#include <errno.h>
#include <locale.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <ntfs-3g/types.h>
#include <ntfs-3g/attrib.h>
#include <ntfs-3g/dir.h>
#include <ntfs-3g/inode.h>
#include <ntfs-3g/layout.h>
#include <ntfs-3g/reparse.h>
#include <ntfs-3g/unistr.h>
#include <ntfs-3g/volume.h>

static ntfschar *wof_name;
static int wof_len;

/* read one NUL-terminated field from stdin; returns NULL at EOF */
static char *field(void)
{
	static char *buf;
	static size_t cap;
	size_t n = 0;
	int c;
	while ((c = getchar()) != EOF && c != 0) {
		if (n + 1 >= cap) {
			cap = cap ? cap * 2 : 4096;
			buf = realloc(buf, cap);
			if (!buf)
				exit(2);
		}
		buf[n++] = (char)c;
	}
	if (c == EOF && n == 0)
		return NULL;
	if (!buf) {
		cap = 16;
		buf = malloc(cap);
	}
	buf[n] = 0;
	return buf;
}

static int do_read(ntfs_volume *vol, const char *outdir)
{
	int bad = 0;
	unsigned long i = 0;
	char *path;
	static char data[1 << 20];
	while ((path = field())) {
		unsigned long id = i++;
		ntfs_inode *ni = ntfs_pathname_to_inode(vol, NULL, path);
		if (!ni) {
			printf("%lu\tERR\topen inode: %s\n", id, strerror(errno));
			bad++;
			continue;
		}
		ntfs_attr *un = ntfs_attr_open(ni, AT_DATA, AT_UNNAMED, 0);
		ntfs_attr *ws = ntfs_attr_open(ni, AT_DATA, wof_name, wof_len);
		if (!un || !ws) {
			printf("%lu\tERR\t%s missing\n", id, !un ? "$DATA" : "WofCompressedData");
			bad++;
			goto next;
		}
		char out[4096];
		snprintf(out, sizeof out, "%s/%lu.wof", outdir, id);
		FILE *f = fopen(out, "wb");
		if (!f) {
			printf("%lu\tERR\tcreate %s: %s\n", id, out, strerror(errno));
			bad++;
			goto next;
		}
		s64 pos = 0, total = ws->data_size;
		int ok = 1;
		while (pos < total) {
			s64 want = total - pos > (s64)sizeof data ? (s64)sizeof data : total - pos;
			s64 got = ntfs_attr_pread(ws, pos, want, data);
			if (got <= 0 || fwrite(data, 1, (size_t)got, f) != (size_t)got) {
				ok = 0;
				break;
			}
			pos += got;
		}
		if (fclose(f) != 0)
			ok = 0;
		if (!ok) {
			printf("%lu\tERR\tread stream\n", id);
			bad++;
		} else {
			printf("%lu\t%lld\t%lld\n", id, (long long)un->data_size, (long long)total);
		}
next:
		if (un)
			ntfs_attr_close(un);
		if (ws)
			ntfs_attr_close(ws);
		ntfs_inode_close(ni);
	}
	return bad;
}

static int replace(ntfs_volume *vol, const char *path, const char *datafile, const char **why)
{
	FILE *f = fopen(datafile, "rb");
	if (!f) {
		*why = "open datafile";
		return -1;
	}
	fseek(f, 0, SEEK_END);
	long n = ftell(f);
	fseek(f, 0, SEEK_SET);
	char *buf = malloc(n > 0 ? (size_t)n : 1);
	if (!buf || (n > 0 && fread(buf, 1, (size_t)n, f) != (size_t)n)) {
		fclose(f);
		free(buf);
		*why = "read datafile";
		return -1;
	}
	fclose(f);

	int rc = -1;
	ntfs_inode *ni = ntfs_pathname_to_inode(vol, NULL, path);
	if (!ni) {
		*why = "open inode";
		free(buf);
		return -1;
	}
	if (!(ni->flags & FILE_ATTR_REPARSE_POINT)) {
		*why = "no reparse point (already done?)";
		goto out;
	}
	/* 1. the reparse point goes first, so a half-done file is never "WOF with bad data" */
	if (ntfs_remove_ntfs_reparse_data(ni)) {
		*why = "remove reparse point";
		goto out;
	}
	/* 2. plain data */
	ntfs_attr *na = ntfs_attr_open(ni, AT_DATA, AT_UNNAMED, 0);
	if (!na) {
		*why = "open $DATA";
		goto out;
	}
	if (ntfs_attr_truncate(na, 0) || (n > 0 && ntfs_attr_pwrite(na, 0, n, buf) != n)) {
		ntfs_attr_close(na);
		*why = "write $DATA";
		goto out;
	}
	ntfs_attr_close(na);
	/* 3. the compressed copy is no longer needed */
	if (ntfs_attr_remove(ni, AT_DATA, wof_name, wof_len)) {
		*why = "remove WofCompressedData";
		goto out;
	}
	ni->flags &= ~FILE_ATTRIBUTE_RECALL_ON_OPEN;
	NInoSetDirty(ni);
	NInoFileNameSetDirty(ni);
	rc = 0;
out:
	if (ntfs_inode_close(ni) && rc == 0) {
		*why = "close inode";
		rc = -1;
	}
	free(buf);
	return rc;
}

static int do_write(ntfs_volume *vol)
{
	int bad = 0;
	char *p;
	while ((p = field())) {
		char *path = strdup(p);
		char *data = field();
		if (!data) {
			printf("ERR\t%s\tmissing datafile\n", path);
			free(path);
			return bad + 1;
		}
		const char *why = "";
		if (replace(vol, path, data, &why)) {
			printf("ERR\t%s\t%s: %s\n", path, why, strerror(errno));
			bad++;
		} else {
			printf("OK\t%s\n", path);
		}
		free(path);
	}
	return bad;
}

int main(int argc, char **argv)
{
	if (argc < 3 || (strcmp(argv[1], "read") && strcmp(argv[1], "write")) ||
	    (!strcmp(argv[1], "read") && argc < 4)) {
		fprintf(stderr, "usage: ntfs-unwof read <device> <outdir> | write <device>  (paths on stdin, NUL-separated)\n");
		return 2;
	}
	setlocale(LC_ALL, "C.UTF-8");
	ntfs_set_char_encoding("C.UTF-8");
	wof_len = ntfs_mbstoucs("WofCompressedData", &wof_name);
	if (wof_len <= 0) {
		fprintf(stderr, "ntfs_mbstoucs failed\n");
		return 2;
	}
	int rd = !strcmp(argv[1], "read");
	ntfs_volume *vol = ntfs_mount(argv[2], rd ? NTFS_MNT_RDONLY : NTFS_MNT_NONE);
	if (!vol) {
		fprintf(stderr, "ntfs_mount %s: %s\n", argv[2], strerror(errno));
		return 2;
	}
	int bad = rd ? do_read(vol, argv[3]) : do_write(vol);
	fflush(stdout);
	if (ntfs_umount(vol, FALSE)) {
		fprintf(stderr, "ntfs_umount: %s\n", strerror(errno));
		return 2;
	}
	return bad ? 1 : 0;
}
