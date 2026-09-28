package runmoor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type InitOptions struct {
	Target         string
	Backend        string
	Auth           string
	CredentialEnv  string
	CredentialFile string
	ClientID       string
	InstallationID int64
	Image          string
	ImageSource    string
	SourceHome     string
	ImageOnly      bool
}

func initialize(path string, opts InitOptions, input io.Reader, output io.Writer, interactive bool) error {
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return problem(ErrConfig, "Configuration already exists or cannot be inspected.", "Choose a new --config path; existing files are never overwritten.")
	}
	reader := bufio.NewReader(input)
	ask := func(label, fallback string) (string, error) {
		fmt.Fprintf(output, "%s", label)
		if fallback != "" {
			fmt.Fprintf(output, " [%s]", fallback)
		}
		fmt.Fprint(output, ": ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", problem(ErrConfig, "Initial setup was interrupted.", "Retry init; no configuration was written.")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = fallback
		}
		return line, nil
	}
	var err error
	if opts.Backend == "" {
		opts.Backend = string(Docker)
		if runtime.GOOS == "darwin" {
			opts.Backend = string(Tart)
		}
		if interactive && !opts.ImageOnly {
			opts.Backend, err = ask("Execution backend (docker/tart)", opts.Backend)
			if err != nil {
				return err
			}
		}
	}
	askAuth := opts.Auth == ""
	if askAuth {
		opts.Auth = string(PAT)
	}
	if interactive && !opts.ImageOnly {
		if opts.Target == "" {
			opts.Target, err = ask("GitHub repository or organization URL", "")
			if err != nil {
				return err
			}
		}
		if askAuth {
			opts.Auth, err = ask("Authentication (pat/app)", opts.Auth)
			if err != nil {
				return err
			}
		}
		if opts.CredentialEnv == "" && opts.CredentialFile == "" {
			kind, e := ask("Credential reference (env/file)", "env")
			if e != nil {
				return e
			}
			if kind == "file" {
				opts.CredentialFile, err = ask("Absolute private credential file", "")
			} else if kind == "env" {
				fallback := "RUNMOOR_PAT"
				if opts.Auth == string(App) {
					fallback = "RUNMOOR_APP_PRIVATE_KEY"
				}
				opts.CredentialEnv, err = ask("Credential environment variable name", fallback)
			} else {
				return problem(ErrConfig, "Unknown credential reference kind.", "Use env or file, never the credential value.")
			}
			if err != nil {
				return err
			}
		}
		if opts.Auth == string(App) {
			if opts.ClientID == "" {
				opts.ClientID, err = ask("GitHub App client ID", "")
				if err != nil {
					return err
				}
			}
			if opts.InstallationID == 0 {
				value, e := ask("GitHub App installation ID", "")
				if e != nil {
					return e
				}
				opts.InstallationID, e = strconv.ParseInt(value, 10, 64)
				if e != nil {
					return problem(ErrConfig, "Invalid installation ID.", "Provide a positive numeric installation ID.")
				}
			}
		}
		if opts.Backend == string(Tart) && opts.Image == "" && opts.ImageSource == "" {
			opts.ImageSource, err = ask("Prepared Tart source (local name, .tvm, sealed UUID or oci:// reference)", "")
			if err != nil {
				return err
			}
		}
	}
	fields := map[string]any{"schema_version": 1}
	if !opts.ImageOnly {
		if opts.Auth == string(App) && (opts.ClientID == "" || opts.InstallationID <= 0) {
			return problem(ErrConfig, "GitHub App setup needs client and installation identifiers.", "Provide --client-id ID and --installation-id N.")
		}
		if opts.Backend == string(Tart) && opts.Image == "" && opts.ImageSource == "" {
			return problem(ErrConfig, "Tart setup needs a prepared macOS source.", "Provide --image-source SOURCE with optional --source-home PATH, or --image SEALED_UUID. Use --image-only to prepare an initial source first.")
		}
		if opts.Target == "" || (opts.CredentialEnv == "" && opts.CredentialFile == "") {
			return problem(ErrConfig, "Initial setup needs a GitHub target and a credential reference.", "Use --target URL and either --credential-env NAME or --credential-file PATH, or run init in a terminal.")
		}
		if (opts.CredentialEnv != "" && opts.CredentialFile != "") || (opts.Backend != string(Docker) && opts.Backend != string(Tart)) {
			return problem(ErrConfig, "Invalid initial setup options.", "Choose docker or tart and exactly one credential reference.")
		}
		credential := map[string]any{}
		if opts.CredentialEnv != "" {
			credential["env"] = opts.CredentialEnv
		} else {
			credential["file"] = opts.CredentialFile
		}
		conn := map[string]any{"name": "project", "target": opts.Target, "auth": opts.Auth, "credential": credential}
		if opts.ClientID != "" {
			conn["client_id"] = opts.ClientID
		}
		if opts.InstallationID != 0 {
			conn["installation_id"] = opts.InstallationID
		}
		name := "linux"
		if opts.Backend == string(Tart) {
			name = "macos"
		}
		pool := map[string]any{"name": name, "connection": "project", "backend": opts.Backend, "scale_set": "runmoor-" + name, "labels": []string{"runmoor-" + name}}
		if opts.Image != "" {
			pool["image"] = opts.Image
		}
		if opts.ImageSource != "" {
			source := map[string]any{"from": opts.ImageSource}
			if opts.SourceHome != "" {
				source["source_home"] = opts.SourceHome
			}
			pool["image_source"] = source
		}
		fields["connections"] = []any{conn}
		fields["pools"] = []any{pool}
	}
	body, err := toml.Marshal(fields)
	if err != nil {
		return err
	}
	var c Config
	if err = toml.Unmarshal(body, &c); err != nil {
		return err
	}
	markAutomatic(&c, fields)
	cap, err := hostCapacity()
	if err != nil {
		return err
	}
	c, err = resolveDefaults(c, cap)
	if err != nil {
		return err
	}
	if err = privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return problem(ErrConfig, "Cannot create configuration exclusively.", "Choose a new --config path.")
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	if _, err = io.WriteString(f, "# Runmoor: omitted resources and runner versions are managed automatically.\n# Credential values stay outside this file.\n"+string(body)); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	success = true
	fmt.Fprintln(output, "Created configuration. "+allocationSummary(c))
	for _, p := range c.Pools {
		fmt.Fprintf(output, "Workflow routing: runs-on: %s\n", p.ScaleSet)
	}
	fmt.Fprintln(output, "Run 'runmoor run' to prepare managed images and start accepting work.")
	return nil
}
