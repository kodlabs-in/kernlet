package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
)

const (
	bundleRoot = "/run/kernlet/bundles"

	manifestMediaType  = "application/vnd.oci.image.manifest.v1+json"
	configMediaType    = "application/vnd.oci.image.config.v1+json"
	layerMediaType     = "application/vnd.oci.image.layer.v1.tar"
	gzipLayerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
)

type Bundle struct {
	Rootfs      string
	Command     []string
	Environment []string
	WorkingDir  string
	UID         uint32
	GID         uint32
}

type layoutFile struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

type index struct {
	SchemaVersion int          `json:"schemaVersion"`
	Manifests     []descriptor `json:"manifests"`
}

type descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *platform `json:"platform,omitempty"`
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type imageConfiguration struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`

	Config struct {
		User       string   `json:"User"`
		Env        []string `json:"Env"`
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
	} `json:"config"`

	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

func Prepare(layoutPath string) (*Bundle, error) {
	layoutPath = filepath.Clean(layoutPath)

	if !filepath.IsAbs(layoutPath) {
		return nil, fmt.Errorf("OCI layout path must be absolute")
	}

	var layout layoutFile

	if err := readJSON(
		filepath.Join(layoutPath, "oci-layout"),
		&layout,
	); err != nil {
		return nil, fmt.Errorf("read OCI layout: %w", err)
	}

	if layout.ImageLayoutVersion != "1.0.0" {
		return nil, fmt.Errorf("unsupported OCI image layout version %q", layout.ImageLayoutVersion)
	}

	var imageIndex index

	if err := readJSON(filepath.Join(layoutPath, "index.json"), &imageIndex); err != nil {
		return nil, fmt.Errorf("read OCI index: %w", err)
	}

	if imageIndex.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported OCI index schema version %d", imageIndex.SchemaVersion)
	}

	manifestDescriptor, err := selectManifest(imageIndex.Manifests)
	if err != nil {
		return nil, err
	}

	manifestBlob, err := readBlob(layoutPath, manifestDescriptor)
	if err != nil {
		return nil, fmt.Errorf("read OCI manifest: %w", err)
	}

	var imageManifest manifest

	if err := json.Unmarshal(manifestBlob, &imageManifest); err != nil {
		return nil, fmt.Errorf("decode OCI manifest: %w", err)
	}

	if imageManifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported manifest schema version %d", imageManifest.SchemaVersion)
	}

	if imageManifest.Config.MediaType != configMediaType {
		return nil, fmt.Errorf("unsupported config media type %q", imageManifest.Config.MediaType)
	}

	if len(imageManifest.Layers) != 1 {
		return nil, fmt.Errorf("OCI V1 requires exactly one layer, received %d", len(imageManifest.Layers))
	}

	configBlob, err := readBlob(layoutPath, imageManifest.Config)
	if err != nil {
		return nil, fmt.Errorf("read OCI configuration: %w", err)
	}

	var imageConfig imageConfiguration

	if err := json.Unmarshal(configBlob, &imageConfig); err != nil {
		return nil, fmt.Errorf("decode OCI configuration: %w", err)
	}

	if imageConfig.OS != "linux" {
		return nil, fmt.Errorf("image OS must be linux")
	}

	if imageConfig.Architecture != goruntime.GOARCH {
		return nil, fmt.Errorf("image architecture %q does not match guest architecture %q", imageConfig.Architecture, goruntime.GOARCH)
	}

	if imageConfig.RootFS.Type != "layers" {
		return nil, fmt.Errorf("unsupported rootfs type %q", imageConfig.RootFS.Type)
	}

	if len(imageConfig.RootFS.DiffIDs) != 1 {
		return nil, fmt.Errorf("OCI V1 requires exactly one diff ID, received %d", len(imageConfig.RootFS.DiffIDs))
	}

	uid, gid, err := parseUser(imageConfig.Config.User)
	if err != nil {
		return nil, err
	}

	command := make([]string, 0, len(imageConfig.Config.Entrypoint)+len(imageConfig.Config.Cmd))

	command = append(command, imageConfig.Config.Entrypoint...)
	command = append(command, imageConfig.Config.Cmd...)

	if len(command) == 0 {
		return nil, fmt.Errorf("image does not define an entrypoint or command")
	}

	if err := validateEnvironment(imageConfig.Config.Env); err != nil {
		return nil, err
	}

	workingDirectory := imageConfig.Config.WorkingDir

	if workingDirectory == "" {
		workingDirectory = "/"
	}

	workingDirectory = filepath.Clean(workingDirectory)

	if !filepath.IsAbs(workingDirectory) {
		return nil, fmt.Errorf("image working directory must be absolute")
	}

	layerDescriptor := imageManifest.Layers[0]

	if layerDescriptor.MediaType != layerMediaType && layerDescriptor.MediaType != gzipLayerMediaType {
		return nil, fmt.Errorf("unsupported layer media type %q", layerDescriptor.MediaType)
	}

	layerBlob, err := readBlob(layoutPath, layerDescriptor)
	if err != nil {
		return nil, fmt.Errorf("read OCI layer: %w", err)
	}

	if err := os.MkdirAll(bundleRoot, 0700); err != nil {
		return nil, fmt.Errorf("create bundle directory: %w", err)
	}

	rootfs, err := os.MkdirTemp(bundleRoot, "rootfs-")
	if err != nil {
		return nil, fmt.Errorf("create bundle rootfs: %w", err)
	}

	if err := unpackLayer(rootfs, layerBlob, layerDescriptor.MediaType, imageConfig.RootFS.DiffIDs[0]); err != nil {
		_ = os.RemoveAll(rootfs)
		return nil, err
	}

	return &Bundle{
		Rootfs:      rootfs,
		Command:     command,
		Environment: append([]string(nil), imageConfig.Config.Env...),
		WorkingDir:  workingDirectory,
		UID:         uid,
		GID:         gid,
	}, nil
}

func (bundle *Bundle) Close() error {
	if bundle == nil || bundle.Rootfs == "" {
		return nil
	}

	relative, err := filepath.Rel(bundleRoot, bundle.Rootfs)
	if err != nil {
		return fmt.Errorf("resolve bundle path: %w", err)
	}

	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove invalid bundle path %q", bundle.Rootfs)
	}

	return os.RemoveAll(bundle.Rootfs)
}

func selectManifest(manifests []descriptor) (descriptor, error) {
	var selected *descriptor

	for index := range manifests {
		candidate := manifests[index]

		if candidate.MediaType != manifestMediaType {
			continue
		}

		if candidate.Platform != nil && (candidate.Platform.OS != "linux" || candidate.Platform.Architecture != goruntime.GOARCH) {
			continue
		}

		if selected != nil {
			return descriptor{}, fmt.Errorf("OCI index contains multiple linux/%s manifests", goruntime.GOARCH)
		}

		selected = &candidate
	}

	if selected == nil {
		return descriptor{}, fmt.Errorf("OCI index does not contain a linux/%s manifest", goruntime.GOARCH)
	}

	return *selected, nil
}

func readJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, destination); err != nil {
		return err
	}

	return nil
}

func readBlob(layoutPath string, value descriptor) ([]byte, error) {
	algorithm, encodedDigest, found := strings.Cut(value.Digest, ":")

	if !found || algorithm != "sha256" {
		return nil, fmt.Errorf("unsupported digest %q", value.Digest)
	}

	if len(encodedDigest) != sha256.Size*2 {
		return nil, fmt.Errorf("invalid SHA-256 digest %q", value.Digest)
	}

	if _, err := hex.DecodeString(encodedDigest); err != nil {
		return nil, fmt.Errorf("decode digest %q: %w", value.Digest, err)
	}

	path := filepath.Join(layoutPath, "blobs", algorithm, encodedDigest)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if int64(len(data)) != value.Size {
		return nil, fmt.Errorf("blob %s has size %d; descriptor requires %d", value.Digest, len(data), value.Size)
	}

	actual := sha256.Sum256(data)
	actualDigest := hex.EncodeToString(actual[:])

	if actualDigest != encodedDigest {
		return nil, fmt.Errorf("blob %s digest mismatch; received sha256:%s", value.Digest, actualDigest)
	}

	return data, nil
}

func unpackLayer(rootfs string, blob []byte, mediaType string, expectedDiffID string) error {
	var layerReader io.Reader = bytes.NewReader(blob)

	var gzipReader *gzip.Reader

	if mediaType == gzipLayerMediaType {
		var err error

		gzipReader, err = gzip.NewReader(layerReader)
		if err != nil {
			return fmt.Errorf("open gzip layer: %w", err)
		}

		layerReader = gzipReader
	}

	hasher := sha256.New()
	hashedReader := io.TeeReader(layerReader, hasher)

	root, err := os.OpenRoot(rootfs)
	if err != nil {
		return fmt.Errorf("open bundle root: %w", err)
	}

	defer root.Close()

	if err := extractArchive(root, tar.NewReader(hashedReader)); err != nil {
		return err
	}

	if _, err := io.Copy(io.Discard, hashedReader); err != nil {
		return fmt.Errorf("finish reading layer: %w", err)
	}

	if gzipReader != nil {
		if err := gzipReader.Close(); err != nil {
			return fmt.Errorf("close gzip layer: %w", err)
		}
	}

	actualDiffID := "sha256:" + hex.EncodeToString(hasher.Sum(nil))

	if actualDiffID != expectedDiffID {
		return fmt.Errorf("layer diff ID mismatch: expected %s, received %s", expectedDiffID, actualDiffID)
	}

	return nil
}

func extractArchive(root *os.Root, archive *tar.Reader) error {
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read layer entry: %w", err)
		}

		name, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}

		if name == "." {
			if header.Typeflag != tar.TypeDir {
				return fmt.Errorf(
					"root layer entry must be a directory, received type %d",
					header.Typeflag,
				)
			}

			if err := applyMetadata(root, name, header, false); err != nil {
				return fmt.Errorf("apply root directory metadata: %w", err)
			}

			continue
		}

		if strings.HasPrefix(filepath.Base(name), ".wh.") {
			return fmt.Errorf("whiteout %q requires multi-layer image support", header.Name)
		}

		if err := createArchiveEntry(root, archive, header, name); err != nil {
			return fmt.Errorf("extract %q: %w", header.Name, err)
		}
	}
}

func createArchiveEntry(root *os.Root, archive *tar.Reader, header *tar.Header, name string) error {
	parent := filepath.Dir(name)

	if parent != "." {
		if err := root.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("create parent directory: %w", err)
		}
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(name, 0755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}

		return applyMetadata(root, name, header, false)

	case tar.TypeReg, tar.TypeRegA:
		if err := root.RemoveAll(name); err != nil {
			return fmt.Errorf("remove existing path: %w", err)
		}

		file, err := root.OpenFile(
			name,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			os.FileMode(header.Mode)&0777,
		)
		if err != nil {
			return fmt.Errorf("create file: %w", err)
		}

		written, copyErr := io.Copy(file, archive)
		closeErr := file.Close()

		if copyErr != nil {
			return fmt.Errorf("write file: %w", copyErr)
		}

		if closeErr != nil {
			return fmt.Errorf("close file: %w", closeErr)
		}

		if written != header.Size {
			return fmt.Errorf("expected %d bytes, wrote %d", header.Size, written)
		}

		return applyMetadata(root, name, header, false)

	case tar.TypeSymlink:
		if err := validateSymlink(name, header.Linkname); err != nil {
			return err
		}

		if err := root.RemoveAll(name); err != nil {
			return fmt.Errorf("remove existing path: %w", err)
		}

		if err := root.Symlink(header.Linkname, name); err != nil {
			return fmt.Errorf("create symbolic link: %w", err)
		}

		return applyMetadata(root, name, header, true)

	case tar.TypeLink:
		target, err := cleanArchivePath(header.Linkname)
		if err != nil {
			return fmt.Errorf("invalid hard-link target: %w", err)
		}

		if target == "." {
			return fmt.Errorf("hard-link target cannot be the root directory")
		}

		if err := root.RemoveAll(name); err != nil {
			return fmt.Errorf("remove existing path: %w", err)
		}

		if err := root.Link(target, name); err != nil {
			return fmt.Errorf("create hard link: %w", err)
		}

		return applyMetadata(root, name, header, false)

	default:
		return fmt.Errorf("unsupported layer entry type %d", header.Typeflag)
	}
}

func cleanArchivePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("layer entry path is empty")
	}

	if filepath.IsAbs(name) {
		return "", fmt.Errorf("layer entry path %q is absolute", name)
	}

	cleaned := filepath.Clean(name)

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("layer entry path %q escapes the rootfs", name)
	}

	return cleaned, nil
}

func validateSymlink(name string, target string) error {
	if target == "" {
		return fmt.Errorf("symbolic-link target is empty")
	}

	if filepath.IsAbs(target) {
		return fmt.Errorf("absolute symbolic-link target %q is unsupported", target)
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(name), target))

	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("symbolic-link target %q escapes the rootfs", target)
	}

	return nil
}

func applyMetadata(root *os.Root, name string, header *tar.Header, symbolicLink bool) error {
	if err := root.Lchown(name, header.Uid, header.Gid); err != nil {
		return fmt.Errorf("set ownership: %w", err)
	}

	if symbolicLink {
		return nil
	}

	mode := os.FileMode(header.Mode & 0777)

	if header.Mode&04000 != 0 {
		mode |= os.ModeSetuid
	}

	if header.Mode&02000 != 0 {
		mode |= os.ModeSetgid
	}

	if header.Mode&01000 != 0 {
		mode |= os.ModeSticky
	}

	if err := root.Chmod(name, mode); err != nil {
		return fmt.Errorf("set mode: %w", err)
	}

	return nil
}

func parseUser(value string) (uint32, uint32, error) {
	uidValue, gidValue, found := strings.Cut(value, ":")

	if !found || uidValue == "" || gidValue == "" {
		return 0, 0, fmt.Errorf("image user must use numeric UID:GID form")
	}

	uid, err := strconv.ParseUint(uidValue, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("parse image UID %q: %w", uidValue, err)
	}

	gid, err := strconv.ParseUint(gidValue, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("parse image GID %q: %w", gidValue, err)
	}

	if uid == 0 || gid == 0 {
		return 0, 0, fmt.Errorf("image UID and GID must be nonzero")
	}

	return uint32(uid), uint32(gid), nil
}

func validateEnvironment(environment []string) error {
	for _, value := range environment {
		name, _, found := strings.Cut(value, "=")

		if !found || name == "" {
			return fmt.Errorf("invalid image environment entry %q", value)
		}

		if strings.IndexByte(name, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("image environment contains a null byte")
		}
	}

	return nil
}
