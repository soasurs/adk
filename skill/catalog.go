package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Load reads and validates a skill directory from fsys. It also indexes
// bundled regular files for use by NewReadResourceTool.
func Load(fsys fs.FS, dir string) (Skill, error) {
	if fsys == nil {
		return Skill{}, fmt.Errorf("skill: load: filesystem must not be nil")
	}
	if !fs.ValidPath(dir) {
		return Skill{}, fmt.Errorf("skill: load: invalid directory %q", dir)
	}

	data, err := fs.ReadFile(fsys, path.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, fmt.Errorf("skill: load %q: read SKILL.md: %w", dir, err)
	}
	skill, err := Parse(data)
	if err != nil {
		return Skill{}, fmt.Errorf("skill: load %q: %w", dir, err)
	}
	if skill.Name != path.Base(dir) {
		return Skill{}, fmt.Errorf("skill: load %q: name %q must match directory name", dir, skill.Name)
	}

	resources, err := discoverResources(fsys, dir)
	if err != nil {
		return Skill{}, fmt.Errorf("skill: load %q: discover resources: %w", dir, err)
	}
	skill.Resources = resources
	resourceIndex := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		resourceIndex[resource] = struct{}{}
	}
	skill.source = &source{
		fsys:      fsys,
		directory: dir,
		resources: resourceIndex,
	}
	return skill, nil
}

// Discover loads skills from the direct child directories of each root.
// Invalid skills and duplicate names cause discovery to fail.
func Discover(fsys fs.FS, roots ...string) (*Catalog, error) {
	if fsys == nil {
		return nil, fmt.Errorf("skill: discover: filesystem must not be nil")
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("skill: discover: at least one root is required")
	}

	var skills []Skill
	for _, root := range roots {
		if !fs.ValidPath(root) {
			return nil, fmt.Errorf("skill: discover: invalid root %q", root)
		}
		entries, err := fs.ReadDir(fsys, root)
		if err != nil {
			return nil, fmt.Errorf("skill: discover root %q: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := path.Join(root, entry.Name())
			_, err := fs.Stat(fsys, path.Join(dir, "SKILL.md"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("skill: discover %q: stat SKILL.md: %w", dir, err)
			}
			skill, err := Load(fsys, dir)
			if err != nil {
				return nil, err
			}
			skills = append(skills, skill)
		}
	}
	return NewCatalog(skills...)
}

// Catalog is an immutable, name-indexed collection of skills.
type Catalog struct {
	skills []Skill
	byName map[string]int
}

// NewCatalog constructs a catalog and rejects invalid or duplicate skills.
func NewCatalog(skills ...Skill) (*Catalog, error) {
	catalog := &Catalog{
		skills: make([]Skill, len(skills)),
		byName: make(map[string]int, len(skills)),
	}
	seen := make(map[string]struct{}, len(skills))
	for i, candidate := range skills {
		candidate = cloneSkill(candidate)
		if candidate.source != nil {
			candidate.Resources = candidate.Resources[:0]
			for resource := range candidate.source.resources {
				candidate.Resources = append(candidate.Resources, resource)
			}
		}
		slices.Sort(candidate.Resources)
		if err := validateSkill(candidate); err != nil {
			return nil, fmt.Errorf("skill: build catalog: %w", err)
		}
		if _, exists := seen[candidate.Name]; exists {
			return nil, fmt.Errorf("skill: build catalog: duplicate skill name %q", candidate.Name)
		}
		seen[candidate.Name] = struct{}{}
		catalog.skills[i] = candidate
	}
	slices.SortFunc(catalog.skills, func(a, b Skill) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	for i := range catalog.skills {
		catalog.byName[catalog.skills[i].Name] = i
	}
	return catalog, nil
}

// Skills returns an isolated copy of the catalog's skills, sorted by name.
func (c *Catalog) Skills() []Skill {
	if c == nil {
		return nil
	}
	skills := make([]Skill, len(c.skills))
	for i := range c.skills {
		skills[i] = cloneSkill(c.skills[i])
	}
	return skills
}

func (c *Catalog) lookup(name string) (Skill, bool) {
	if c == nil {
		return Skill{}, false
	}
	index, ok := c.byName[name]
	if !ok {
		return Skill{}, false
	}
	return cloneSkill(c.skills[index]), true
}

func discoverResources(fsys fs.FS, dir string) ([]string, error) {
	var resources []string
	err := fs.WalkDir(fsys, dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == dir {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative := strings.TrimPrefix(name, dir+"/")
		if relative == "SKILL.md" {
			return nil
		}
		resources = append(resources, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(resources)
	return resources, nil
}
