package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
)

type parserSection int

const (
	parserSectionRoot parserSection = iota
	parserSectionDaemon
	parserSectionFolder
	parserSectionFolderJob
)

type tomlValueType int

const (
	tomlValueString tomlValueType = iota
	tomlValueBool
	tomlValueInt
)

type tomlValue struct {
	valueType tomlValueType
	stringVal string
	boolVal   bool
	intVal    int
}

func Load(path string) (*Config, error) {
	configPath := strings.TrimSpace(path)
	if configPath == "" {
		return nil, fmt.Errorf("config path is required")
	}

	absPath, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	payload, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := defaultConfig()
	if err := parseTOML(payload, &cfg); err != nil {
		return nil, err
	}
	if err := resolveFolderPaths(&cfg, filepath.Dir(absPath)); err != nil {
		return nil, err
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func parseTOML(payload []byte, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	normalized := strings.ReplaceAll(string(payload), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	section := parserSectionRoot
	var currentFolder *FolderConfig
	var currentJob *JobConfig

	for idx := 0; idx < len(lines); idx++ {
		line := strings.TrimSpace(lines[idx])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch line {
		case "[daemon]":
			section = parserSectionDaemon
			currentJob = nil
			continue
		case "[[folder]]":
			section = parserSectionFolder
			cfg.Folders = append(cfg.Folders, defaultFolderConfig())
			currentFolder = &cfg.Folders[len(cfg.Folders)-1]
			currentJob = nil
			continue
		case "[[folder.job]]":
			section = parserSectionFolderJob
			if currentFolder == nil {
				return fmt.Errorf("line %d: [[folder.job]] requires a previous [[folder]]", idx+1)
			}
			currentFolder.Jobs = append(currentFolder.Jobs, defaultJobConfig())
			currentJob = &currentFolder.Jobs[len(currentFolder.Jobs)-1]
			continue
		}

		key, value, nextIndex, err := parseAssignment(lines, idx)
		if err != nil {
			return fmt.Errorf("line %d: %w", idx+1, err)
		}
		idx = nextIndex

		switch section {
		case parserSectionRoot:
			if key != "version" {
				return fmt.Errorf("line %d: unsupported root key: %s", idx+1, key)
			}
			if value.valueType != tomlValueInt {
				return fmt.Errorf("line %d: version must be an integer", idx+1)
			}
			cfg.Version = value.intVal
		case parserSectionDaemon:
			if err := applyDaemonValue(&cfg.Daemon, key, value, idx+1); err != nil {
				return err
			}
		case parserSectionFolder:
			if currentFolder == nil {
				return fmt.Errorf("line %d: [[folder]] section is not initialized", idx+1)
			}
			if err := applyFolderValue(currentFolder, key, value, idx+1); err != nil {
				return err
			}
		case parserSectionFolderJob:
			if currentJob == nil {
				return fmt.Errorf("line %d: [[folder.job]] section is not initialized", idx+1)
			}
			if err := applyJobValue(currentJob, key, value, idx+1); err != nil {
				return err
			}
		default:
			return fmt.Errorf("line %d: unsupported parser section", idx+1)
		}
	}

	return nil
}

func resolveFolderPaths(cfg *Config, configDir string) error {
	for folderIndex := range cfg.Folders {
		folderPath := strings.TrimSpace(cfg.Folders[folderIndex].Path)
		if folderPath == "" {
			continue
		}

		candidatePath := folderPath
		if !filepath.IsAbs(candidatePath) {
			candidatePath = filepath.Join(configDir, candidatePath)
		}

		absolutePath, err := filepath.Abs(filepath.Clean(candidatePath))
		if err != nil {
			return fmt.Errorf("resolve folder path for %s: %w", cfg.Folders[folderIndex].ID, err)
		}
		cfg.Folders[folderIndex].Path = absolutePath
	}

	return nil
}

func applyDaemonValue(daemon *DaemonConfig, key string, value tomlValue, lineNumber int) error {
	switch key {
	case "max_concurrent_jobs":
		if value.valueType != tomlValueInt {
			return fmt.Errorf("line %d: daemon.max_concurrent_jobs must be an integer", lineNumber)
		}
		daemon.MaxConcurrentJobs = value.intVal
	case "startup_run":
		if value.valueType != tomlValueBool {
			return fmt.Errorf("line %d: daemon.startup_run must be a boolean", lineNumber)
		}
		daemon.StartupRun = value.boolVal
	case "log_level":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: daemon.log_level must be a string", lineNumber)
		}
		daemon.LogLevel = value.stringVal
	default:
		return fmt.Errorf("line %d: unsupported daemon key: %s", lineNumber, key)
	}
	return nil
}

func applyFolderValue(folder *FolderConfig, key string, value tomlValue, lineNumber int) error {
	switch key {
	case "id":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.id must be a string", lineNumber)
		}
		folder.ID = value.stringVal
	case "path":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.path must be a string", lineNumber)
		}
		folder.Path = value.stringVal
	default:
		return fmt.Errorf("line %d: unsupported folder key: %s", lineNumber, key)
	}
	return nil
}

func applyJobValue(job *JobConfig, key string, value tomlValue, lineNumber int) error {
	switch key {
	case "id":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.id must be a string", lineNumber)
		}
		job.ID = value.stringVal
	case "type":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.type must be a string", lineNumber)
		}
		job.Type = contracts.DevmonJobType(value.stringVal)
	case "enabled":
		if value.valueType != tomlValueBool {
			return fmt.Errorf("line %d: folder.job.enabled must be a boolean", lineNumber)
		}
		job.Enabled = value.boolVal
	case "interval":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.interval must be a string", lineNumber)
		}
		job.Interval = value.stringVal
	case "timeout":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.timeout must be a string", lineNumber)
		}
		job.Timeout = value.stringVal
	case "shell":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.shell must be a string", lineNumber)
		}
		job.Shell = value.stringVal
	case "script":
		if value.valueType != tomlValueString {
			return fmt.Errorf("line %d: folder.job.script must be a string", lineNumber)
		}
		job.Script = value.stringVal
	case "startup_run":
		if value.valueType != tomlValueBool {
			return fmt.Errorf("line %d: folder.job.startup_run must be a boolean", lineNumber)
		}
		startup := value.boolVal
		job.StartupRun = &startup
	default:
		return fmt.Errorf("line %d: unsupported folder.job key: %s", lineNumber, key)
	}
	return nil
}

func parseAssignment(lines []string, index int) (string, tomlValue, int, error) {
	trimmed := strings.TrimSpace(lines[index])
	equalsIndex := strings.Index(trimmed, "=")
	if equalsIndex <= 0 {
		return "", tomlValue{}, index, fmt.Errorf("invalid key/value assignment")
	}

	key := strings.TrimSpace(trimmed[:equalsIndex])
	if key == "" {
		return "", tomlValue{}, index, fmt.Errorf("assignment key is empty")
	}

	rawValue := strings.TrimSpace(trimmed[equalsIndex+1:])
	if rawValue == "" {
		return "", tomlValue{}, index, fmt.Errorf("assignment value is empty")
	}

	if strings.HasPrefix(rawValue, "\"\"\"") || strings.HasPrefix(rawValue, "'''") {
		text, nextIndex, err := parseMultilineString(lines, index, rawValue)
		if err != nil {
			return "", tomlValue{}, nextIndex, err
		}
		return key, tomlValue{valueType: tomlValueString, stringVal: text}, nextIndex, nil
	}

	if strings.HasPrefix(rawValue, "\"") {
		text, err := parseBasicString(rawValue)
		if err != nil {
			return "", tomlValue{}, index, err
		}
		return key, tomlValue{valueType: tomlValueString, stringVal: text}, index, nil
	}

	if strings.HasPrefix(rawValue, "'") {
		text, err := parseLiteralString(rawValue)
		if err != nil {
			return "", tomlValue{}, index, err
		}
		return key, tomlValue{valueType: tomlValueString, stringVal: text}, index, nil
	}

	bareValue := stripInlineComment(rawValue)
	switch bareValue {
	case "true":
		return key, tomlValue{valueType: tomlValueBool, boolVal: true}, index, nil
	case "false":
		return key, tomlValue{valueType: tomlValueBool, boolVal: false}, index, nil
	default:
		intValue, err := strconv.Atoi(bareValue)
		if err != nil {
			return "", tomlValue{}, index, fmt.Errorf("unsupported value syntax: %s", rawValue)
		}
		return key, tomlValue{valueType: tomlValueInt, intVal: intValue}, index, nil
	}
}

func parseMultilineString(lines []string, index int, rawValue string) (string, int, error) {
	delimiter := rawValue[:3]
	remainder := rawValue[3:]

	var builder strings.Builder
	if delimiterEnd := strings.Index(remainder, delimiter); delimiterEnd >= 0 {
		builder.WriteString(remainder[:delimiterEnd])
		trailing := strings.TrimSpace(remainder[delimiterEnd+3:])
		if trailing != "" && !strings.HasPrefix(trailing, "#") {
			return "", index, fmt.Errorf("unexpected trailing content after multiline string")
		}
		value := builder.String()
		if strings.HasPrefix(value, "\n") {
			value = strings.TrimPrefix(value, "\n")
		}
		return value, index, nil
	}

	builder.WriteString(remainder)
	for lineIndex := index + 1; lineIndex < len(lines); lineIndex++ {
		builder.WriteString("\n")
		segment := lines[lineIndex]
		if delimiterEnd := strings.Index(segment, delimiter); delimiterEnd >= 0 {
			builder.WriteString(segment[:delimiterEnd])
			trailing := strings.TrimSpace(segment[delimiterEnd+3:])
			if trailing != "" && !strings.HasPrefix(trailing, "#") {
				return "", lineIndex, fmt.Errorf("unexpected trailing content after multiline string")
			}
			value := builder.String()
			if strings.HasPrefix(value, "\n") {
				value = strings.TrimPrefix(value, "\n")
			}
			return value, lineIndex, nil
		}
		builder.WriteString(segment)
	}

	return "", len(lines) - 1, fmt.Errorf("unterminated multiline string")
}

func parseBasicString(rawValue string) (string, error) {
	escaped := false
	for index := 1; index < len(rawValue); index++ {
		current := rawValue[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if current == '"' {
			token := rawValue[:index+1]
			trailing := strings.TrimSpace(rawValue[index+1:])
			if trailing != "" && !strings.HasPrefix(trailing, "#") {
				return "", fmt.Errorf("unexpected trailing content after string")
			}
			value, err := strconv.Unquote(token)
			if err != nil {
				return "", fmt.Errorf("invalid escaped string: %w", err)
			}
			return value, nil
		}
	}

	return "", fmt.Errorf("unterminated string")
}

func parseLiteralString(rawValue string) (string, error) {
	for index := 1; index < len(rawValue); index++ {
		if rawValue[index] != '\'' {
			continue
		}
		value := rawValue[1:index]
		trailing := strings.TrimSpace(rawValue[index+1:])
		if trailing != "" && !strings.HasPrefix(trailing, "#") {
			return "", fmt.Errorf("unexpected trailing content after string")
		}
		return value, nil
	}

	return "", fmt.Errorf("unterminated string")
}

func stripInlineComment(rawValue string) string {
	commentIndex := strings.Index(rawValue, "#")
	if commentIndex == -1 {
		return strings.TrimSpace(rawValue)
	}
	return strings.TrimSpace(rawValue[:commentIndex])
}
