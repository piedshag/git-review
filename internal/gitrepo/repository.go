package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const (
	maxReadLines = 400
	maxMatches   = 100
)

// Snapshot exposes only immutable Git objects at the base and target commits.
// It never reads files from the checked-out worktree.
type Snapshot struct {
	repository *git.Repository
	base       *object.Commit
	target     *object.Commit
	baseName   string
	targetName string
}

func Open(repoPath, baseRevision, targetRevision string) (*Snapshot, error) {
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	target, err := resolveCommit(repo, targetRevision)
	if err != nil {
		return nil, fmt.Errorf("resolve target %q: %w", targetRevision, err)
	}
	baseName := baseRevision
	var base *object.Commit
	if baseRevision != "" {
		base, err = resolveCommit(repo, baseRevision)
	} else {
		for _, candidate := range []string{"origin/main", "main", "origin/master", "master"} {
			if candidate == targetRevision {
				continue
			}
			base, err = resolveCommit(repo, candidate)
			if err == nil {
				baseName = candidate
				break
			}
		}
	}
	if base == nil {
		if baseRevision == "" {
			return nil, errors.New("could not auto-detect a base revision; pass --base")
		}
		return nil, fmt.Errorf("resolve base %q: %w", baseRevision, err)
	}

	mergeBases, mergeErr := base.MergeBase(target)
	if mergeErr == nil && len(mergeBases) > 0 {
		// Compare the topic branch with the point where it diverged, avoiding
		// unrelated changes added to the base branch later.
		base = mergeBases[0]
	}

	return &Snapshot{repository: repo, base: base, target: target, baseName: baseName, targetName: targetRevision}, nil
}

func resolveCommit(repo *git.Repository, revision string) (*object.Commit, error) {
	candidates := []string{revision, "refs/heads/" + revision, "refs/remotes/" + revision, "refs/tags/" + revision}
	var lastErr error
	for _, candidate := range candidates {
		hash, err := repo.ResolveRevision(plumbing.Revision(candidate))
		if err != nil {
			lastErr = err
			continue
		}
		commit, err := repo.CommitObject(*hash)
		if err == nil {
			return commit, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *Snapshot) Description() string {
	return fmt.Sprintf("target %s (%s), base %s at merge-base (%s)", s.targetName, s.target.Hash, s.baseName, s.base.Hash)
}

type selectedPatch struct {
	files []fdiff.FilePatch
}

func (p selectedPatch) FilePatches() []fdiff.FilePatch { return p.files }
func (selectedPatch) Message() string                  { return "" }

func (s *Snapshot) Diff(ctx context.Context, name string, contextLines *int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	context := fdiff.DefaultContextLines
	if contextLines != nil {
		context = *contextLines
	}
	if context < 1 || context > 20 {
		return "", errors.New("context must be between 1 and 20")
	}
	clean := ""
	if name != "" {
		clean = path.Clean(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return "", errors.New("path must be a repository-relative file path")
		}
	}
	patch, err := s.base.Patch(s.target)
	if err != nil {
		return "", err
	}
	files := patch.FilePatches()
	if clean != "" {
		files = nil
		for _, filePatch := range patch.FilePatches() {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			from, to := filePatch.Files()
			if (from != nil && from.Path() == clean) || (to != nil && to.Path() == clean) {
				files = append(files, filePatch)
			}
		}
	}
	if len(files) == 0 {
		if clean == "" {
			return "No changes.", nil
		}
		return fmt.Sprintf("No changes for %q.", clean), nil
	}
	var output strings.Builder
	if err := fdiff.NewUnifiedEncoder(&output, context).Encode(selectedPatch{files: files}); err != nil {
		return "", fmt.Errorf("encode diff: %w", err)
	}
	return output.String(), nil
}

func (s *Snapshot) commit(ref string) (*object.Commit, error) {
	switch ref {
	case "", "target":
		return s.target, nil
	case "base":
		return s.base, nil
	default:
		return nil, errors.New("ref must be target or base")
	}
}

func (s *Snapshot) Stat(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	patch, err := s.base.Patch(s.target)
	if err != nil {
		return "", err
	}
	counts := make(map[string]object.FileStat)
	for _, stat := range patch.Stats() {
		counts[stat.Name] = stat
	}
	var lines []string
	for _, filePatch := range patch.FilePatches() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		from, to := filePatch.Files()
		status, name := "M", ""
		switch {
		case from == nil:
			status, name = "A", to.Path()
		case to == nil:
			status, name = "D", from.Path()
		default:
			name = to.Path()
			if from.Path() != to.Path() {
				status = "R"
			}
		}
		stat := counts[name]
		lines = append(lines, fmt.Sprintf("%s\t+%d\t-%d\t%s", status, stat.Addition, stat.Deletion, name))
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return "No changes.", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Snapshot) Glob(ctx context.Context, pattern, ref string) (string, error) {
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	matcher, err := compileGlob(pattern)
	if err != nil {
		return "", err
	}
	commit, err := s.commit(ref)
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	var paths []string
	err = tree.Files().ForEach(func(file *object.File) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if matcher.MatchString(file.Name) {
			paths = append(paths, file.Name)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "No matching files.", nil
	}
	return strings.Join(paths, "\n"), nil
}

func (s *Snapshot) Grep(ctx context.Context, pattern, fileGlob, ref string) (string, error) {
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	search, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}
	var matcher *regexp.Regexp
	if fileGlob != "" {
		matcher, err = compileGlob(fileGlob)
		if err != nil {
			return "", err
		}
	}
	commit, err := s.commit(ref)
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	var matches []string
	err = tree.Files().ForEach(func(file *object.File) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(matches) >= maxMatches || (matcher != nil && !matcher.MatchString(file.Name)) {
			return nil
		}
		reader, err := file.Reader()
		if err != nil {
			return err
		}
		defer reader.Close()
		scanner := bufio.NewScanner(io.LimitReader(reader, 2*1024*1024))
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		line := 0
		for scanner.Scan() && len(matches) < maxMatches {
			if err := ctx.Err(); err != nil {
				return err
			}
			line++
			text := scanner.Text()
			if strings.IndexByte(text, 0) >= 0 {
				break
			}
			if search.MatchString(text) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", file.Name, line, text))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "No matches.", nil
	}
	return strings.Join(matches, "\n"), nil
}

func (s *Snapshot) Read(ctx context.Context, name, ref string, start, end int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	clean := path.Clean(name)
	if name == "" || clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", errors.New("path must be a repository-relative file path")
	}
	commit, err := s.commit(ref)
	if err != nil {
		return "", err
	}
	file, err := commit.File(clean)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", clean, err)
	}
	contents, err := file.Contents()
	if err != nil {
		return "", err
	}
	if bytes.IndexByte([]byte(contents), 0) >= 0 {
		return "", errors.New("binary files cannot be read")
	}
	lines := strings.Split(contents, "\n")
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", errors.New("end must be greater than or equal to start")
	}
	if end-start+1 > maxReadLines {
		end = start + maxReadLines - 1
	}
	if start > len(lines) {
		return "", fmt.Errorf("start line %d is past end of file (%d lines)", start, len(lines))
	}
	var out strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&out, "%6d\t%s\n", i, lines[i-1])
	}
	return out.String(), nil
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > 1024 {
		return nil, errors.New("glob is too long")
	}
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					out.WriteString("(?:.*/)?")
					i++
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	out.WriteString("$")
	return regexp.Compile(out.String())
}
