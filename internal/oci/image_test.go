package oci

import (
	"archive/tar"
	"bytes"
	"os"
	"testing"
)

func TestCleanArchivePathRejectsEscapes(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"/etc/passwd",
		"../escape",
		"../../escape",
		"directory/../../../escape",
	}

	for _, value := range invalid {
		value := value

		t.Run(value, func(t *testing.T) {
			t.Parallel()

			if _, err := cleanArchivePath(value); err == nil {
				t.Fatalf("cleanArchivePath(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestCleanArchivePathAcceptsRootfsPath(t *testing.T) {
	t.Parallel()

	actual, err := cleanArchivePath("./sbin/kernlet-agent")
	if err != nil {
		t.Fatal(err)
	}

	if actual != "sbin/kernlet-agent" {
		t.Fatalf("expected sbin/kernlet-agent, received %q", actual)
	}
}

func TestExtractArchiveAppliesRootDirectoryMode(t *testing.T) {
	rootfs := t.TempDir()

	if err := os.Chmod(rootfs, 0700); err != nil {
		t.Fatal(err)
	}

	var layer bytes.Buffer

	writer := tar.NewWriter(&layer)

	if err := writer.WriteHeader(&tar.Header{
		Name:     "./",
		Typeflag: tar.TypeDir,
		Mode:     0755,
		Uid:      os.Getuid(),
		Gid:      os.Getgid(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(rootfs)
	if err != nil {
		t.Fatal(err)
	}

	defer root.Close()

	if err := extractArchive(
		root,
		tar.NewReader(bytes.NewReader(layer.Bytes())),
	); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(rootfs)
	if err != nil {
		t.Fatal(err)
	}

	if actual := info.Mode().Perm(); actual != 0755 {
		t.Fatalf("expected rootfs mode 0755, received %04o", actual)
	}
}
