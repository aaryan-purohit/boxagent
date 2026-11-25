package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

var errBadVersion = errors.New("--version must be at least 1")

// everything is the argument that selects all files.
const everything = "."

type openStoreFunc func(context.Context) (*vfs.Store, error)

func newVFSCmd(open openStoreFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vfs",
		Short: "Inspect files the agent has saved",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newVFSListCmd(open), newVFSCatCmd(open), newVFSHistoryCmd(open), newVFSExportCmd(open), newVFSRemoveCmd(open))
	return cmd
}

func newVFSListCmd(open openStoreFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [prefix]",
		Short: "List files with their latest version and size",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := everything
			if len(args) == 1 {
				raw = args[0]
			}
			prefix, err := parsePrefix(raw)
			if err != nil {
				return err
			}
			return withStore(cmd.Context(), open, func(s *vfs.Store) error {
				files, err := s.List(cmd.Context(), prefix)
				if err != nil {
					return err
				}
				return printFiles(cmd.OutOrStdout(), files)
			})
		},
	}
}

func newVFSCatCmd(open openStoreFunc) *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "cat <path>",
		Short: "Print a file's content (control characters are removed on a terminal)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("version") && version < 1 {
				return errBadVersion
			}
			p, err := vfs.ParsePath(args[0])
			if err != nil {
				return err
			}
			return withStore(cmd.Context(), open, func(s *vfs.Store) error {
				content, _, err := s.Read(cmd.Context(), p, version)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if isTerminal(out) {
					content = []byte(stripControl(string(content)))
				}
				_, err = out.Write(content)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&version, "version", 0, "version to print (default latest)")
	return cmd
}

func newVFSHistoryCmd(open openStoreFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "history <path>",
		Short: "List a file's versions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := vfs.ParsePath(args[0])
			if err != nil {
				return err
			}
			return withStore(cmd.Context(), open, func(s *vfs.Store) error {
				versions, err := s.History(cmd.Context(), p)
				if err != nil {
					return err
				}
				return printHistory(cmd.OutOrStdout(), versions)
			})
		},
	}
}

// parsePrefix parses a command-line prefix, where "." means everything. An
// empty one is an error, since it is usually an unset shell variable.
func parsePrefix(raw string) (vfs.Prefix, error) {
	if raw == everything {
		return vfs.Prefix{}, nil
	}
	p, err := vfs.ParsePath(raw)
	if err != nil {
		return vfs.Prefix{}, err
	}
	return vfs.ParsePrefix(p.String())
}

// isTerminal reports whether w is a character device, such as a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func withStore(ctx context.Context, open openStoreFunc, use func(*vfs.Store) error) error {
	s, err := open(ctx)
	if err != nil {
		return err
	}
	return errors.Join(use(s), s.Close())
}

func printFiles(w io.Writer, files []vfs.Version) error {
	tw := newTable(w)
	_, _ = fmt.Fprintln(tw, "PATH\tVERSION\tSIZE") // Flush reports write errors
	for _, f := range files {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\n", printable(f.Path), f.Version, f.Size)
	}
	return tw.Flush()
}

func printHistory(w io.Writer, versions []vfs.Version) error {
	tw := newTable(w)
	_, _ = fmt.Fprintln(tw, "VERSION\tHASH\tSIZE\tCREATED\tSESSION") // Flush reports write errors
	for _, v := range versions {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n",
			v.Version, v.Hash, v.Size, v.CreatedAt.Format(time.RFC3339), v.SessionID)
	}
	return tw.Flush()
}

func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// printable quotes s if it holds control characters, because the model picks
// paths and raw escape sequences could manipulate the user's terminal.
func printable(s string) string {
	if q := strconv.Quote(s); q != `"`+s+`"` {
		return q
	}
	return s
}
