package registry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
)

var defaultRegistryDir = filepath.Join(os.Getenv("HOME"), ".aether", "registry")

type index map[string]map[string]*core.RegistryEntry

type Registry struct {
	dir     string
	pkgsDir string
	idxPath string
	idx     index
}

func New(dir string) *Registry {
	if dir == "" {
		dir = defaultRegistryDir
	}
	pkgsDir := filepath.Join(dir, "packages")
	os.MkdirAll(pkgsDir, 0755)
	r := &Registry{
		dir:     dir,
		pkgsDir: pkgsDir,
		idxPath: filepath.Join(dir, "index.json"),
		idx:     make(index),
	}
	r.load()
	return r
}

func (r *Registry) Publish(
	artifactPath, name, version, description, author string,
	tags []string,
) (*core.RegistryEntry, error) {
	if _, err := os.Stat(artifactPath); err != nil {
		return nil, fmt.Errorf("artifact not found: %s", artifactPath)
	}
	if !strings.HasSuffix(artifactPath, ".ae") {
		return nil, fmt.Errorf("expected .ae file, got: %s", artifactPath)
	}
	if r.idx[name] != nil && r.idx[name][version] != nil {
		return nil, fmt.Errorf("package %s@%s already exists in registry", name, version)
	}

	destDir := filepath.Join(r.pkgsDir, name, version)
	os.MkdirAll(destDir, 0755)
	destPath := filepath.Join(destDir, filepath.Base(artifactPath))

	src, err := os.Open(artifactPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	io.Copy(dst, src)

	// Checksum
	src.Seek(0, 0)
	h := sha256.New()
	io.Copy(h, src)
	checksum := fmt.Sprintf("%x", h.Sum(nil))

	info, _ := os.Stat(artifactPath)
	entry := &core.RegistryEntry{
		Name:         name,
		Version:      version,
		Description:  description,
		Author:       author,
		PublishedAt:  time.Now().UTC().Format(time.RFC3339),
		ArtifactPath: destPath,
		SizeBytes:    info.Size(),
		Checksum:     checksum,
		Tags:         tags,
		Downloads:    0,
	}

	if r.idx[name] == nil {
		r.idx[name] = make(map[string]*core.RegistryEntry)
	}
	r.idx[name][version] = entry
	r.save()
	return entry, nil
}

func (r *Registry) Pull(name, version, destDir string) (string, error) {
	if r.idx[name] == nil {
		return "", fmt.Errorf("package '%s' not found in registry", name)
	}
	if version == "" {
		versions := make([]string, 0, len(r.idx[name]))
		for v := range r.idx[name] {
			versions = append(versions, v)
		}
		sort.Strings(versions)
		version = versions[len(versions)-1]
	}
	entry := r.idx[name][version]
	if entry == nil {
		return "", fmt.Errorf("version '%s' of '%s' not found", version, name)
	}

	src, err := os.Open(entry.ArtifactPath)
	if err != nil {
		return "", fmt.Errorf("artifact file missing: %s", entry.ArtifactPath)
	}
	defer src.Close()

	destPath := filepath.Join(destDir, filepath.Base(entry.ArtifactPath))
	dst, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	io.Copy(dst, src)

	entry.Downloads++
	r.save()
	return destPath, nil
}

func (r *Registry) Search(query string, tags []string) []*core.RegistryEntry {
	q := strings.ToLower(query)
	var results []*core.RegistryEntry
	for _, versions := range r.idx {
		for _, entry := range versions {
			if q != "" && !strings.Contains(strings.ToLower(entry.Name), q) &&
				!strings.Contains(strings.ToLower(entry.Description), q) {
				continue
			}
			if len(tags) > 0 {
				matched := false
				for _, t := range tags {
					for _, et := range entry.Tags {
						if strings.EqualFold(t, et) {
							matched = true
							break
						}
					}
				}
				if !matched {
					continue
				}
			}
			results = append(results, entry)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Downloads != results[j].Downloads {
			return results[i].Downloads > results[j].Downloads
		}
		return results[i].Name < results[j].Name
	})
	return results
}

func (r *Registry) ListVersions(name string) []string {
	if r.idx[name] == nil {
		return nil
	}
	versions := make([]string, 0, len(r.idx[name]))
	for v := range r.idx[name] {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return versions
}

func (r *Registry) Info(name, version string) *core.RegistryEntry {
	if r.idx[name] == nil {
		return nil
	}
	if version == "" {
		versions := r.ListVersions(name)
		if len(versions) == 0 {
			return nil
		}
		version = versions[len(versions)-1]
	}
	return r.idx[name][version]
}

func (r *Registry) Stats() map[string]int {
	totalPkgs := len(r.idx)
	totalVersions := 0
	totalDownloads := 0
	for _, versions := range r.idx {
		totalVersions += len(versions)
		for _, e := range versions {
			totalDownloads += e.Downloads
		}
	}
	return map[string]int{
		"total_packages":  totalPkgs,
		"total_versions":  totalVersions,
		"total_downloads": totalDownloads,
	}
}

func (r *Registry) save() {
	data, _ := json.MarshalIndent(r.idx, "", "  ")
	os.WriteFile(r.idxPath, data, 0644)
}

func (r *Registry) load() {
	data, err := os.ReadFile(r.idxPath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &r.idx)
}
