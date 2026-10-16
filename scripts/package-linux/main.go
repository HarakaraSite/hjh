// Command package-linux builds the Linux/amd64 distribution archive.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const archiveName = "hjh-linux-amd64.tar.gz"

var moduleNotices = map[string][]string{
	"github.com/go-quickjs/go-quickjs": {"internal/fdlibm/LICENSE"},
}

type options struct {
	out        string
	version    string
	releaseDir string
}

type binaryResult struct {
	Build             string               `json:"build"`
	ELFProgramHeaders []elfProgramHeader   `json:"elfProgramHeaders"`
	ELF               elfSummary           `json:"elf"`
	GoDependencies    []goModuleDependency `json:"goDependencies"`
	Bytes             int64                `json:"bytes"`
	SHA256            string               `json:"sha256"`
}

type elfSummary struct {
	Class   string `json:"class"`
	Data    string `json:"data"`
	OSABI   string `json:"osABI"`
	Type    string `json:"type"`
	Machine string `json:"machine"`
	Entry   uint64 `json:"entry"`
}

type elfProgramHeader struct {
	Type            string `json:"type"`
	Flags           string `json:"flags"`
	Offset          uint64 `json:"offset"`
	VirtualAddress  uint64 `json:"virtualAddress"`
	PhysicalAddress uint64 `json:"physicalAddress"`
	FileSize        uint64 `json:"fileSize"`
	MemorySize      uint64 `json:"memorySize"`
	Alignment       uint64 `json:"alignment"`
}

type elfData struct {
	summary elfSummary
	headers []elfProgramHeader
}

type dependency struct {
	Module  string   `json:"module"`
	Version string   `json:"version"`
	Sum     string   `json:"sum"`
	Notices []string `json:"notices"`
}

type goModuleDependency struct {
	Module  string `json:"module"`
	Version string `json:"version"`
	Sum     string `json:"sum"`
}

type moduleInfo struct {
	Path    string      `json:"Path"`
	Version string      `json:"Version"`
	Sum     string      `json:"Sum"`
	Dir     string      `json:"Dir"`
	Replace *moduleInfo `json:"Replace"`
}

func main() {
	root, err := repositoryRoot()
	if err != nil {
		fatal(err)
	}
	args, err := parseOptions(root, os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fatal(err)
	}
	result, err := packageDistribution(root, args)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fatal(err)
	}
}

func repositoryRoot() (string, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot determine package-linux source directory")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(source), "..", ".."))
}

func parseOptions(root string, args []string) (options, error) {
	set := flag.NewFlagSet("package-linux", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	out := set.String("out", filepath.Join(root, "dist", "packages"), "Directory for package build artifacts")
	version := set.String("version", "", "Version to embed in both binaries, for example v1.0.0")
	releaseDir := set.String("release-dir", "", "Also copy the archive to this release upload directory")
	if err := set.Parse(args); err != nil {
		return options{}, err
	}
	if set.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	resolve := func(path string) (string, error) {
		if path == "" {
			return "", nil
		}
		return filepath.Abs(path)
	}
	resolvedOut, err := resolve(*out)
	if err != nil {
		return options{}, err
	}
	resolvedReleaseDir, err := resolve(*releaseDir)
	if err != nil {
		return options{}, err
	}
	return options{out: resolvedOut, version: *version, releaseDir: resolvedReleaseDir}, nil
}

func packageDistribution(root string, args options) (map[string]any, error) {
	revision, dirty, err := sourceMetadata(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(args.out, 0o777); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	work, err := os.MkdirTemp(args.out, "build-")
	if err != nil {
		return nil, fmt.Errorf("create package work directory: %w", err)
	}
	app := filepath.Join(work, "hjh-linux-amd64")
	if err := os.MkdirAll(app, 0o777); err != nil {
		return nil, fmt.Errorf("create app directory: %w", err)
	}

	env := targetEnvironment(os.Environ())
	ldflags := strings.Join([]string{
		"-X hjh/internal/buildinfo.SourceRevision=" + revision,
		"-X hjh/internal/buildinfo.SourceDirty=" + fmt.Sprint(dirty),
	}, " ")
	if args.version != "" {
		ldflags += " -X hjh/internal/buildinfo.Version=" + args.version
	}

	targets := []struct {
		name string
		pkg  string
	}{
		{name: "hjh", pkg: "./cmd/hjh"},
		{name: "hjh-tui", pkg: "./cmd/hjh-tui"},
	}
	binaries := make(map[string]binaryResult, len(targets))
	for _, target := range targets {
		binaryPath := filepath.Join(app, target.name)
		if err := runCommand(root, env, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", binaryPath, target.pkg); err != nil {
			return nil, fmt.Errorf("build %s: %w", target.name, err)
		}
		build, err := buildinfo.ReadFile(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("read %s Go build information: %w", target.name, err)
		}
		elfData, err := inspectELF(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("inspect %s ELF: %w", target.name, err)
		}
		size, digest, err := fileDigest(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", target.name, err)
		}
		binaries[target.name] = binaryResult{
			Build:             build.String(),
			ELFProgramHeaders: elfData.headers,
			ELF:               elfData.summary,
			GoDependencies:    dependenciesFromBuild(build),
			Bytes:             size,
			SHA256:            digest,
		}
	}

	modules, err := listModules(root)
	if err != nil {
		return nil, err
	}
	dependencies, err := collectLicenses(app, binaries, modules)
	if err != nil {
		return nil, err
	}
	goroot, err := goRoot(root)
	if err != nil {
		return nil, err
	}
	goLicenses := filepath.Join(app, "licenses", "go")
	if err := os.MkdirAll(goLicenses, 0o777); err != nil {
		return nil, fmt.Errorf("create Go license directory: %w", err)
	}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		if err := copyFile(filepath.Join(goroot, name), filepath.Join(goLicenses, name)); err != nil {
			return nil, fmt.Errorf("copy Go %s: %w", name, err)
		}
	}
	if err := copyTree(filepath.Join(root, "internal", "extensions", "assets"), filepath.Join(app, "extensions")); err != nil {
		return nil, fmt.Errorf("copy editable extensions: %w", err)
	}
	if err := copyTree(filepath.Join(root, "spikes", "s01", "s03-tools", "vendor"), filepath.Join(app, "extensions", "vendor")); err != nil {
		return nil, fmt.Errorf("copy prepared extension dependencies: %w", err)
	}
	modulesDir := filepath.Join(app, "extensions", "modules")
	if err := os.MkdirAll(modulesDir, 0o777); err != nil {
		return nil, fmt.Errorf("create extension modules directory: %w", err)
	}
	if err := copyFile(filepath.Join(root, "spikes", "s01", "s03-tools", "modules", "catalog.js"), filepath.Join(modulesDir, "catalog.js")); err != nil {
		return nil, fmt.Errorf("copy catalog module: %w", err)
	}
	if err := copyFile(filepath.Join(root, "docs", "distribution-linux.md"), filepath.Join(app, "README.md")); err != nil {
		return nil, fmt.Errorf("copy distribution instructions: %w", err)
	}
	if err := copyFile(filepath.Join(root, "LICENSE"), filepath.Join(app, "LICENSE")); err != nil {
		return nil, fmt.Errorf("copy project license: %w", err)
	}
	notice := "hjh Linux/amd64 distribution: hjh core and optional hjh-tui client.\n" +
		"Project license: LICENSE (MIT).\n" +
		"Go module and runtime licenses: licenses/.\n" +
		"Optional YAML and semver ESM licenses: extensions/vendor/.\n" +
		"Per-binary dependencies and file hashes: manifest.json.\n"
	if err := os.WriteFile(filepath.Join(app, "NOTICE"), []byte(notice), 0o666); err != nil {
		return nil, fmt.Errorf("write distribution notice: %w", err)
	}

	files, err := fileManifest(app)
	if err != nil {
		return nil, err
	}
	jsManifestBytes, err := os.ReadFile(filepath.Join(app, "extensions", "vendor", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read prepared JavaScript dependency manifest: %w", err)
	}
	var jsManifest struct {
		Dependencies json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(jsManifestBytes, &jsManifest); err != nil {
		return nil, fmt.Errorf("parse prepared JavaScript dependency manifest: %w", err)
	}
	manifest := map[string]any{
		"target":         "linux/amd64",
		"source":         map[string]any{"revision": revision, "dirty": dirty},
		"binaries":       binaries,
		"goDependencies": dependencies,
		"jsDependencies": json.RawMessage(jsManifest.Dependencies),
		"files":          files,
	}
	if args.version != "" {
		manifest["version"] = args.version
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(app, "manifest.json"), manifestBytes, 0o666); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	archive := filepath.Join(work, archiveName)
	if err := writeArchive(app, archive); err != nil {
		return nil, err
	}
	archiveSize, archiveDigest, err := fileDigest(archive)
	if err != nil {
		return nil, fmt.Errorf("hash package archive: %w", err)
	}
	resultBinaries := make(map[string]any, len(binaries))
	for name, binary := range binaries {
		resultBinaries[name] = map[string]any{"sha256": binary.SHA256, "bytes": binary.Bytes}
	}
	result := map[string]any{
		"archive":  archive,
		"sha256":   archiveDigest,
		"bytes":    archiveSize,
		"binaries": resultBinaries,
	}
	if args.version != "" {
		result["version"] = args.version
	}
	packageBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode package result: %w", err)
	}
	packageBytes = append(packageBytes, '\n')
	if err := os.WriteFile(filepath.Join(work, "package.json"), packageBytes, 0o666); err != nil {
		return nil, fmt.Errorf("write package result: %w", err)
	}
	if args.releaseDir != "" {
		if err := os.MkdirAll(args.releaseDir, 0o777); err != nil {
			return nil, fmt.Errorf("create release directory: %w", err)
		}
		releaseArchive := filepath.Join(args.releaseDir, archiveName)
		if err := copyFile(archive, releaseArchive); err != nil {
			return nil, fmt.Errorf("copy release archive: %w", err)
		}
		result["releaseArchive"] = releaseArchive
	}
	return result, nil
}

func targetEnvironment(parent []string) []string {
	values := make(map[string]string, len(parent)+4)
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	values["CGO_ENABLED"] = "0"
	values["GOOS"] = "linux"
	values["GOARCH"] = "amd64"
	values["GOAMD64"] = "v1"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func sourceMetadata(root string) (string, bool, error) {
	revision, err := commandOutput(root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", false, fmt.Errorf("read source revision: %w", err)
	}
	status, err := commandOutput(root, nil, "git", "status", "--porcelain")
	if err != nil {
		return "", false, fmt.Errorf("read source status: %w", err)
	}
	return strings.TrimSpace(revision), strings.TrimSpace(status) != "", nil
}

func runCommand(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func commandOutput(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var stderr strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	output, err := cmd.Output()
	if err != nil {
		if stderr.Len() != 0 {
			return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(output), nil
}

func dependenciesFromBuild(info *buildinfo.BuildInfo) []goModuleDependency {
	dependencies := make([]goModuleDependency, 0, len(info.Deps))
	for _, module := range info.Deps {
		if module == nil {
			continue
		}
		dependencies = append(dependencies, goModuleDependency{Module: module.Path, Version: module.Version, Sum: module.Sum})
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Module < dependencies[j].Module })
	return dependencies
}

func inspectELF(path string) (elfData, error) {
	file, err := elf.Open(path)
	if err != nil {
		return elfData{}, err
	}
	defer file.Close()
	summary := elfSummary{Class: file.Class.String(), Data: file.Data.String(), OSABI: file.OSABI.String(), Type: file.Type.String(), Machine: file.Machine.String(), Entry: file.Entry}
	headers := make([]elfProgramHeader, 0, len(file.Progs))
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			interpreter, _ := io.ReadAll(program.Open())
			return elfData{}, fmt.Errorf("requires ELF interpreter %q", strings.TrimRight(string(interpreter), "\x00"))
		}
		headers = append(headers, elfProgramHeader{Type: program.Type.String(), Flags: program.Flags.String(), Offset: program.Off,
			VirtualAddress: program.Vaddr, PhysicalAddress: program.Paddr, FileSize: program.Filesz, MemorySize: program.Memsz, Alignment: program.Align})
	}
	return elfData{summary: summary, headers: headers}, nil
}

func listModules(root string) (map[string]moduleInfo, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = root
	cmd.Stdout = nil
	var stderr strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list Go modules: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	modules := make(map[string]moduleInfo)
	decoder := json.NewDecoder(strings.NewReader(string(stdout)))
	for {
		var module moduleInfo
		if err := decoder.Decode(&module); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode Go module list: %w", err)
		}
		modules[module.Path] = module
	}
	return modules, nil
}

func collectLicenses(app string, binaries map[string]binaryResult, modules map[string]moduleInfo) ([]dependency, error) {
	linked := make(map[string]dependency)
	for _, binary := range binaries {
		for _, dep := range binary.GoDependencies {
			linked[dep.Module] = dependency{Module: dep.Module, Version: dep.Version, Sum: dep.Sum}
		}
	}
	names := make([]string, 0, len(linked))
	for name := range linked {
		names = append(names, name)
	}
	sort.Strings(names)
	dependencies := make([]dependency, 0, len(names))
	for _, name := range names {
		dep := linked[name]
		module, ok := modules[name]
		if !ok {
			return nil, fmt.Errorf("Go module source not found for %s", name)
		}
		source := module.Dir
		if module.Replace != nil && module.Replace.Dir != "" {
			source = module.Replace.Dir
		}
		if source == "" {
			return nil, fmt.Errorf("Go module source directory not found for %s", name)
		}
		destination := filepath.Join(app, "licenses", filepath.FromSlash(name))
		if err := os.MkdirAll(destination, 0o777); err != nil {
			return nil, fmt.Errorf("create license directory for %s: %w", name, err)
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return nil, fmt.Errorf("read Go module source for %s: %w", name, err)
		}
		for _, entry := range entries {
			upper := strings.ToUpper(entry.Name())
			if !hasNoticePrefix(upper) {
				continue
			}
			info, err := os.Stat(filepath.Join(source, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("stat Go module notice %s: %w", entry.Name(), err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if err := copyFile(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return nil, fmt.Errorf("copy Go module notice %s: %w", entry.Name(), err)
			}
			dep.Notices = append(dep.Notices, filepath.ToSlash(filepath.Join("licenses", name, entry.Name())))
		}
		for _, relative := range moduleNotices[name] {
			if err := copyFile(filepath.Join(source, filepath.FromSlash(relative)), filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
				return nil, fmt.Errorf("copy Go module notice %s/%s: %w", name, relative, err)
			}
			notice := filepath.ToSlash(filepath.Join("licenses", name, relative))
			dep.Notices = append(dep.Notices, notice)
		}
		if len(dep.Notices) == 0 {
			return nil, fmt.Errorf("no upstream license material: %s", name)
		}
		dependencies = append(dependencies, dep)
	}
	return dependencies, nil
}

func hasNoticePrefix(name string) bool {
	for _, prefix := range []string{"LICENSE", "COPYING", "NOTICE", "AUTHORS", "CONTRIBUTORS", "PATENTS"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func goRoot(root string) (string, error) {
	value, err := commandOutput(root, nil, "go", "env", "GOROOT")
	if err != nil {
		return "", fmt.Errorf("find Go runtime licenses: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func copyTree(source, destination string) error {
	type copiedDirectory struct {
		path string
		mode os.FileMode
	}
	var directories []copiedDirectory
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o777); err != nil {
				return err
			}
			directories = append(directories, copiedDirectory{path: target, mode: info.Mode().Perm()})
			return nil
		}
		if info.Mode().IsRegular() {
			if err := copyFile(path, target); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		return fmt.Errorf("unsupported extension asset type %s", path)
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func fileManifest(root string) (map[string]any, error) {
	paths, err := filesUnder(root)
	if err != nil {
		return nil, err
	}
	manifest := make(map[string]any)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		size, digest, err := fileDigest(path)
		if err != nil {
			return nil, err
		}
		manifest[filepath.ToSlash(relative)] = map[string]any{"bytes": size, "sha256": digest}
	}
	return manifest, nil
}

func filesUnder(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && path != root {
			paths = append(paths, path)
		}
		return err
	})
	sort.Slice(paths, func(i, j int) bool {
		left, _ := filepath.Rel(root, paths[i])
		right, _ := filepath.Rel(root, paths[j])
		return left < right
	})
	return paths, err
}

func fileDigest(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func writeArchive(app, destination string) error {
	paths, err := filesUnder(app)
	if err != nil {
		return fmt.Errorf("walk package files: %w", err)
	}
	archive, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create package archive: %w", err)
	}
	compressed, err := gzip.NewWriterLevel(archive, gzip.BestCompression)
	if err != nil {
		archive.Close()
		return fmt.Errorf("create gzip writer: %w", err)
	}
	compressed.Name = ""
	compressed.ModTime = time.Unix(0, 0).UTC()
	compressed.OS = 255
	tarWriter := tar.NewWriter(compressed)
	allPaths := append([]string{app}, paths...)
	for _, path := range allPaths {
		info, err := os.Lstat(path)
		if err != nil {
			return closeArchive(archive, compressed, tarWriter, fmt.Errorf("stat archive path %s: %w", path, err))
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return closeArchive(archive, compressed, tarWriter, fmt.Errorf("create archive header for %s: %w", path, err))
		}
		name := "hjh-linux-amd64"
		if path != app {
			relative, err := filepath.Rel(app, path)
			if err != nil {
				return closeArchive(archive, compressed, tarWriter, err)
			}
			name = filepath.ToSlash(filepath.Join(name, relative))
		}
		header.Name = name
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		header.ModTime = time.Unix(0, 0).UTC()
		header.AccessTime, header.ChangeTime = time.Time{}, time.Time{}
		header.Format = tar.FormatPAX
		if err := tarWriter.WriteHeader(header); err != nil {
			return closeArchive(archive, compressed, tarWriter, fmt.Errorf("write archive header for %s: %w", name, err))
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return closeArchive(archive, compressed, tarWriter, fmt.Errorf("open archive file %s: %w", path, err))
			}
			_, copyErr := io.Copy(tarWriter, file)
			closeErr := file.Close()
			if copyErr != nil {
				return closeArchive(archive, compressed, tarWriter, fmt.Errorf("write archive file %s: %w", path, copyErr))
			}
			if closeErr != nil {
				return closeArchive(archive, compressed, tarWriter, fmt.Errorf("close archive file %s: %w", path, closeErr))
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		compressed.Close()
		archive.Close()
		return fmt.Errorf("finish tar archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		archive.Close()
		return fmt.Errorf("finish gzip archive: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close package archive: %w", err)
	}
	return nil
}

func closeArchive(archive *os.File, compressed *gzip.Writer, tarWriter *tar.Writer, cause error) error {
	_ = tarWriter.Close()
	_ = compressed.Close()
	_ = archive.Close()
	return cause
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "package-linux: %v\n", err)
	os.Exit(1)
}
