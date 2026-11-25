package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

var (
	errNotEmpty = errors.New("directory is not empty")
	errNoFiles  = errors.New("no saved files match")
	errNeedYes  = errors.New("nothing deleted; repeat with --yes to delete every version of these files permanently")
)

func newVFSExportCmd(open openStoreFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "export <directory> [prefix]",
		Short: "Copy the latest version of saved files into a new or empty directory",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := everything
			if len(args) == 2 {
				raw = args[1]
			}
			return withStore(cmd.Context(), open, func(s *vfs.Store) error {
				files, _, err := findFiles(cmd.Context(), s, raw)
				if err != nil {
					return err
				}
				if err := export(cmd.Context(), s, files, args[0]); err != nil {
					return fmt.Errorf("export to %q: %w", args[0], err)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "exported %d files\n", len(files))
				return err
			})
		},
	}
}

func newVFSRemoveCmd(open openStoreFunc) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <prefix>",
		Short: "Permanently delete every version of saved files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(cmd.Context(), open, func(s *vfs.Store) error {
				files, prefix, err := findFiles(cmd.Context(), s, args[0])
				if err != nil {
					return err
				}
				if err := printFiles(cmd.OutOrStdout(), files); err != nil {
					return err
				}
				if !yes {
					return errNeedYes
				}
				return s.Delete(cmd.Context(), prefix)
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "delete without asking again")
	return cmd
}

// findFiles lists the files under a command-line prefix, of which there must be
// at least one.
func findFiles(ctx context.Context, s *vfs.Store, raw string) ([]vfs.Version, vfs.Prefix, error) {
	prefix, err := parsePrefix(raw)
	if err != nil {
		return nil, vfs.Prefix{}, err
	}
	files, err := s.List(ctx, prefix)
	if err == nil && len(files) == 0 {
		err = fmt.Errorf("%w %q", errNoFiles, raw)
	}
	return files, prefix, err
}

// export writes files under dir. It never overwrites, and os.Root keeps it
// inside dir.
func export(ctx context.Context, s *vfs.Store, files []vfs.Version, dir string) (err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return errNotEmpty
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for _, f := range files {
		if err := exportFile(ctx, s, f, root); err != nil {
			return err
		}
	}
	return nil
}

func exportFile(ctx context.Context, s *vfs.Store, v vfs.Version, root *os.Root) error {
	p, err := vfs.ParsePath(v.Path)
	if err != nil {
		return err
	}
	content, _, err := s.Read(ctx, p, v.Version)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.FromSlash(path.Dir(v.Path)), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(filepath.FromSlash(v.Path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	return errors.Join(err, f.Close())
}
