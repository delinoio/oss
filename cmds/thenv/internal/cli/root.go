package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/thenv/internal/client"
	"github.com/delinoio/oss/pkg/thenv/api"
	"github.com/spf13/cobra"
)

const (
	defaultServerURL      = "http://127.0.0.1:8080"
	defaultEnvOutputPath  = ".env"
	defaultVarsOutputPath = ".dev.vars"
)

type ScopeFlags struct {
	WorkspaceID   string
	ProjectID     string
	EnvironmentID string
}

type RootOptions struct {
	ServerURL string
	Token     string
	Verbose   bool
}

var ErrFileConflict = errors.New("target file content conflicts with pulled content")

func NewRootCommand() *cobra.Command {
	options := RootOptions{}

	command := &cobra.Command{
		Use:           "thenv",
		Short:         "thenv securely distributes .env and .dev.vars bundles",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	command.PersistentFlags().StringVar(&options.ServerURL, "server-url", defaultServerURL, "thenv server URL")
	command.PersistentFlags().StringVar(&options.Token, "token", "", "JWT token used for server authentication")
	command.PersistentFlags().BoolVar(&options.Verbose, "verbose", false, "enable verbose logging")

	command.AddCommand(newPushCommand(&options))
	command.AddCommand(newPullCommand(&options))
	command.AddCommand(newListCommand(&options))
	command.AddCommand(newRotateCommand(&options))

	return command
}

func Execute() error {
	return NewRootCommand().Execute()
}

func newPushCommand(options *RootOptions) *cobra.Command {
	scope := ScopeFlags{}
	var envFilePath string
	var devVarsFilePath string

	command := &cobra.Command{
		Use:   "push",
		Short: "push local .env/.dev.vars files as a new immutable bundle version",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := buildLogger(options.Verbose)
			client, err := buildClient(options, logger)
			if err != nil {
				return err
			}

			files, err := collectPushFiles(envFilePath, devVarsFilePath)
			if err != nil {
				return err
			}
			logger.Info("starting push operation", "scope", scope, "file_types", collectFileTypes(files))

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			response, err := client.PushBundleVersion(ctx, api.PushBundleVersionRequest{
				Scope: scope.toScope(),
				Files: files,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Pushed bundle version: %s\n", response.BundleVersionID)
			return nil
		},
	}

	bindScopeFlags(command, &scope)
	command.Flags().StringVar(&envFilePath, "env-file", "", "path to .env file")
	command.Flags().StringVar(&devVarsFilePath, "dev-vars-file", "", "path to .dev.vars file")

	return command
}

func newPullCommand(options *RootOptions) *cobra.Command {
	scope := ScopeFlags{}
	var envOutputPath string
	var devVarsOutputPath string
	var force bool

	command := &cobra.Command{
		Use:   "pull",
		Short: "pull active bundle files and write them locally",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := buildLogger(options.Verbose)
			client, err := buildClient(options, logger)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			response, err := client.PullActiveBundle(ctx, api.PullActiveBundleRequest{Scope: scope.toScope()})
			if err != nil {
				return err
			}

			if envOutputPath == "" {
				envOutputPath = defaultEnvOutputPath
			}
			if devVarsOutputPath == "" {
				devVarsOutputPath = defaultVarsOutputPath
			}

			written := make([]string, 0, 2)
			for _, file := range response.Files {
				targetPath, err := outputPathForFile(file.FileType, envOutputPath, devVarsOutputPath)
				if err != nil {
					return err
				}
				if err := writeOutputFile(targetPath, file.Content, force); err != nil {
					return fmt.Errorf("write output file %s: %w", targetPath, err)
				}
				written = append(written, fmt.Sprintf("%s (%s)", targetPath, file.FileType.String()))
			}
			sort.Strings(written)
			fmt.Printf("Pulled bundle version: %s\n", response.Version.BundleVersionID)
			for _, output := range written {
				fmt.Printf("- %s\n", output)
			}
			return nil
		},
	}

	bindScopeFlags(command, &scope)
	command.Flags().StringVar(&envOutputPath, "output-env-file", "", "output path for .env")
	command.Flags().StringVar(&devVarsOutputPath, "output-dev-vars-file", "", "output path for .dev.vars")
	command.Flags().BoolVar(&force, "force", false, "overwrite outputs even when existing content differs")

	return command
}

func newListCommand(options *RootOptions) *cobra.Command {
	scope := ScopeFlags{}
	var limit uint32
	var cursor string

	command := &cobra.Command{
		Use:   "list",
		Short: "list bundle versions for a scope",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := buildLogger(options.Verbose)
			client, err := buildClient(options, logger)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			response, err := client.ListBundleVersions(ctx, api.ListBundleVersionsRequest{
				Scope:  scope.toScope(),
				Limit:  limit,
				Cursor: cursor,
			})
			if err != nil {
				return err
			}
			for _, version := range response.Versions {
				fmt.Printf("%s\t%s\t%s\t%s\n", version.BundleVersionID, version.Status.String(), version.CreatedBy, version.CreatedAt.Format(time.RFC3339))
			}
			if response.NextCursor != "" {
				fmt.Printf("next_cursor=%s\n", response.NextCursor)
			}
			return nil
		},
	}

	bindScopeFlags(command, &scope)
	command.Flags().Uint32Var(&limit, "limit", api.DefaultListLimit, "maximum number of versions to return")
	command.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")

	return command
}

func newRotateCommand(options *RootOptions) *cobra.Command {
	scope := ScopeFlags{}
	var fromVersion string

	command := &cobra.Command{
		Use:   "rotate",
		Short: "create a new version from source version and activate it",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := buildLogger(options.Verbose)
			client, err := buildClient(options, logger)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			response, err := client.RotateBundleVersion(ctx, api.RotateBundleVersionRequest{
				Scope:         scope.toScope(),
				FromVersionID: strings.TrimSpace(fromVersion),
			})
			if err != nil {
				return err
			}
			fmt.Printf("Rotated bundle version: %s\n", response.BundleVersionID)
			return nil
		},
	}

	bindScopeFlags(command, &scope)
	command.Flags().StringVar(&fromVersion, "from-version", "", "source bundle version ID")
	return command
}

func buildClient(options *RootOptions, logger *slog.Logger) (*client.Client, error) {
	token := strings.TrimSpace(options.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("THENV_TOKEN"))
	}
	if token == "" {
		return nil, errors.New("token is required (--token or THENV_TOKEN)")
	}
	return client.New(options.ServerURL, token, logger)
}

func bindScopeFlags(command *cobra.Command, scope *ScopeFlags) {
	command.Flags().StringVar(&scope.WorkspaceID, "workspace", "", "workspace identifier")
	command.Flags().StringVar(&scope.ProjectID, "project", "", "project identifier")
	command.Flags().StringVar(&scope.EnvironmentID, "env", "", "environment identifier")
	_ = command.MarkFlagRequired("workspace")
	_ = command.MarkFlagRequired("project")
	_ = command.MarkFlagRequired("env")
}

func buildLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func collectPushFiles(envFilePath string, devVarsFilePath string) ([]api.BundleFilePayload, error) {
	files := make([]api.BundleFilePayload, 0, 2)
	if strings.TrimSpace(envFilePath) != "" {
		content, err := os.ReadFile(envFilePath)
		if err != nil {
			return nil, fmt.Errorf("read env file: %w", err)
		}
		files = append(files, api.BundleFilePayload{FileType: api.FileTypeEnv, Content: content, ByteLength: int64(len(content)), Checksum: checksum(content)})
	}
	if strings.TrimSpace(devVarsFilePath) != "" {
		content, err := os.ReadFile(devVarsFilePath)
		if err != nil {
			return nil, fmt.Errorf("read dev vars file: %w", err)
		}
		files = append(files, api.BundleFilePayload{FileType: api.FileTypeDevVars, Content: content, ByteLength: int64(len(content)), Checksum: checksum(content)})
	}
	if len(files) == 0 {
		return nil, errors.New("at least one input file is required (--env-file or --dev-vars-file)")
	}
	return files, nil
}

func outputPathForFile(fileType api.FileType, envOutputPath string, devVarsOutputPath string) (string, error) {
	switch fileType {
	case api.FileTypeEnv:
		return envOutputPath, nil
	case api.FileTypeDevVars:
		return devVarsOutputPath, nil
	default:
		return "", fmt.Errorf("unsupported file type in pull response: %d", fileType)
	}
}

func writeOutputFile(path string, content []byte, force bool) error {
	existing, err := os.ReadFile(path)
	if err == nil {
		if !bytes.Equal(existing, content) && !force {
			return ErrFileConflict
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read existing file: %w", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set file mode to 0600: %w", err)
	}
	return nil
}

func collectFileTypes(files []api.BundleFilePayload) []string {
	values := make([]string, 0, len(files))
	for _, file := range files {
		values = append(values, file.FileType.String())
	}
	sort.Strings(values)
	return values
}

func checksum(input []byte) string {
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:])
}

func (s ScopeFlags) toScope() api.Scope {
	return api.Scope{
		WorkspaceID:   strings.TrimSpace(s.WorkspaceID),
		ProjectID:     strings.TrimSpace(s.ProjectID),
		EnvironmentID: strings.TrimSpace(s.EnvironmentID),
	}
}
