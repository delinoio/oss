package runmoor

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const serviceDefinitionLimit = 64 << 10

type serviceDefinitionSnapshot struct {
	data []byte
	info os.FileInfo
}

func requireServiceConfigMatch(goos, unit, requested string) error {
	_, err := validateServiceConfigMatch(goos, unit, requested)
	return err
}

func validateServiceConfigMatch(goos, unit, requested string) (serviceDefinitionSnapshot, error) {
	if goos == "linux" {
		if err := requireNoSystemdDropIns(unit); err != nil {
			return serviceDefinitionSnapshot{}, problem(ErrConfig, "The installed systemd service has an ambiguous effective definition.", "Remove systemd user drop-ins for Runmoor or service units, then restore the generated Runmoor service definition.")
		}
	}
	data, info, err := readPrivateServiceDefinition(unit)
	if err != nil {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The installed service definition is missing or cannot be read securely.", "Restore an owner-only regular Runmoor service definition without symlinks before retrying.")
	}
	installed, err := serviceConfigFromDefinition(goos, data)
	if err != nil {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The installed service definition is invalid or ambiguous.", "Preserve it and restore a valid Runmoor service definition before retrying.")
	}
	installed, err = cleanAbsoluteServicePath(installed)
	if err != nil {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The installed service definition has no safe configuration path.", "Preserve it and restore a valid Runmoor service definition before retrying.")
	}
	if requested == "" || strings.ContainsAny(requested, "\x00\r\n") {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The requested configuration path is invalid.", "Use the same valid --config path used when installing the service.")
	}
	wanted, err := filepath.Abs(requested)
	if err != nil {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The requested configuration path is invalid.", "Use the same valid --config path used when installing the service.")
	}
	if hasParentPathComponent(requested) {
		// filepath.Abs cleans dot segments before resolving symlinks. Compare
		// both real paths so a symlink/.. sequence cannot select a different
		// configuration while harmless aliases such as /var -> /private/var work.
		originalResolved, resolveErr := filepath.EvalSymlinks(requested)
		if resolveErr == nil {
			originalResolved, resolveErr = filepath.Abs(originalResolved)
		}
		cleanedResolved, cleanedErr := filepath.EvalSymlinks(wanted)
		if cleanedErr == nil {
			cleanedResolved, cleanedErr = filepath.Abs(cleanedResolved)
		}
		if resolveErr != nil || cleanedErr != nil || filepath.Clean(originalResolved) != filepath.Clean(cleanedResolved) {
			return serviceDefinitionSnapshot{}, problem(ErrConfig, "The requested configuration path is ambiguous.", "Use the installed absolute --config path without symlink-based dot segments.")
		}
	}
	if filepath.Clean(wanted) != installed {
		return serviceDefinitionSnapshot{}, problem(ErrConfig, "The requested configuration does not match the installed service.", "Use the same --config path used when installing the service; drain and uninstall that service before replacing its configuration.")
	}
	return serviceDefinitionSnapshot{data: data, info: info}, nil
}

func hasParentPathComponent(path string) bool {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return true
		}
	}
	return false
}

func requireServiceDefinitionUnchanged(goos, unit, requested string, original serviceDefinitionSnapshot) error {
	current, err := validateServiceConfigMatch(goos, unit, requested)
	if err != nil {
		return err
	}
	if !os.SameFile(original.info, current.info) || !bytes.Equal(original.data, current.data) {
		return problem(ErrConfig, "The installed service definition changed during the operation.", "Preserve the current definition and retry service stop or uninstall.")
	}
	return nil
}

func readPrivateServiceDefinition(path string) ([]byte, os.FileInfo, error) {
	f, err := openPrivate(path, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, serviceDefinitionLimit+1))
	if err != nil || len(data) > serviceDefinitionLimit {
		return nil, nil, problem(ErrConfig, "Private input exceeds its size limit or cannot be read.", "Use a bounded service definition.")
	}
	return data, info, nil
}

func requireNoSystemdDropIns(unit string) error {
	// systemd merges unit-specific and service-type drop-ins into the effective
	// definition, and a same-named unit in a higher-priority root can replace the
	// installed file. Inspect every standard user lookup root directly so
	// validation can reject either ambiguity before contacting systemd or opening
	// offline state.
	for _, root := range systemdUserUnitDirs(unit) {
		shadow := filepath.Join(root, systemdServiceName)
		if filepath.Clean(shadow) != filepath.Clean(unit) {
			if _, err := os.Lstat(shadow); err == nil {
				return errInvalidServiceDefinition
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, name := range []string{"runmoor.service.d", "service.d"} {
			path := filepath.Join(root, name)
			if _, err := os.Lstat(path); err == nil {
				return errInvalidServiceDefinition
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func systemdUserUnitDirs(unit string) []string {
	var dirs []string
	add := func(path string) {
		if !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		for _, existing := range dirs {
			if existing == path {
				return
			}
		}
		dirs = append(dirs, path)
	}
	add(filepath.Dir(unit))

	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}
	add(filepath.Join(configHome, "systemd", "user.control"))
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if filepath.IsAbs(runtimeDir) {
		add(filepath.Join(runtimeDir, "systemd", "user.control"))
		add(filepath.Join(runtimeDir, "systemd", "transient"))
		add(filepath.Join(runtimeDir, "systemd", "generator.early"))
	}
	add(filepath.Join(configHome, "systemd", "user"))
	configDirs := os.Getenv("XDG_CONFIG_DIRS")
	if configDirs == "" {
		configDirs = "/etc/xdg"
	}
	for _, base := range filepath.SplitList(configDirs) {
		add(filepath.Join(base, "systemd", "user"))
	}
	add("/etc/systemd/user")

	if filepath.IsAbs(runtimeDir) {
		add(filepath.Join(runtimeDir, "systemd", "user"))
	}
	add("/run/systemd/user")
	if filepath.IsAbs(runtimeDir) {
		add(filepath.Join(runtimeDir, "systemd", "generator"))
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dataHome) {
		dataHome = filepath.Join(home, ".local", "share")
	}
	add(filepath.Join(dataHome, "systemd", "user"))
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, base := range filepath.SplitList(dataDirs) {
		add(filepath.Join(base, "systemd", "user"))
	}
	add("/usr/local/lib/systemd/user")
	add("/usr/lib/systemd/user")
	if filepath.IsAbs(runtimeDir) {
		add(filepath.Join(runtimeDir, "systemd", "generator.late"))
	}
	return dirs
}

func serviceConfigFromDefinition(goos string, data []byte) (string, error) {
	if len(data) == 0 || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", errInvalidServiceDefinition
	}
	switch goos {
	case "darwin":
		return launchdServiceConfig(data)
	case "linux":
		return systemdServiceConfig(data)
	default:
		return "", errInvalidServiceDefinition
	}
}

var errInvalidServiceDefinition = errors.New("invalid service definition")

func cleanAbsoluteServicePath(path string) (string, error) {
	if path == "" || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n") || !filepath.IsAbs(path) {
		return "", errInvalidServiceDefinition
	}
	return filepath.Clean(path), nil
}

func launchdServiceConfig(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var root xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", errInvalidServiceDefinition
		}
		switch value := tok.(type) {
		case xml.StartElement:
			root = value
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return "", errInvalidServiceDefinition
			}
		case xml.Directive:
			if string(value) != `DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"` {
				return "", errInvalidServiceDefinition
			}
		case xml.ProcInst:
			if value.Target != "xml" {
				return "", errInvalidServiceDefinition
			}
		default:
			return "", errInvalidServiceDefinition
		}
		if root.Name.Local != "" {
			break
		}
	}
	if root.Name.Local != "plist" || root.Name.Space != "" || len(root.Attr) != 1 || root.Attr[0].Name.Local != "version" || root.Attr[0].Value != "1.0" {
		return "", errInvalidServiceDefinition
	}
	start, err := nextPlistStart(dec)
	if err != nil || start.Name.Local != "dict" || start.Name.Space != "" {
		return "", errInvalidServiceDefinition
	}
	dict, err := decodePlistValue(dec, start)
	if err != nil || dict.kind != "dict" {
		return "", errInvalidServiceDefinition
	}
	end, err := dec.Token()
	if err != nil {
		return "", errInvalidServiceDefinition
	}
	rootEnd, ok := end.(xml.EndElement)
	if !ok || rootEnd.Name != root.Name {
		return "", errInvalidServiceDefinition
	}
	if err := ensurePlistEOF(dec); err != nil {
		return "", errInvalidServiceDefinition
	}
	if !validLaunchdDefinition(dict.dict) {
		return "", errInvalidServiceDefinition
	}
	array := dict.dict["ProgramArguments"].array
	args := make([]string, len(array))
	for i := range array {
		args[i] = array[i].text
	}
	_, config, err := serviceInvocation(args, "run")
	return config, err
}

type plistValue struct {
	kind  string
	text  string
	array []plistValue
	dict  map[string]plistValue
}

const maxPlistNesting = 16

func nextPlistStart(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		switch value := tok.(type) {
		case xml.StartElement:
			return value, nil
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return xml.StartElement{}, errInvalidServiceDefinition
			}
		default:
			return xml.StartElement{}, errInvalidServiceDefinition
		}
	}
}

func decodePlistValue(dec *xml.Decoder, start xml.StartElement) (plistValue, error) {
	return decodePlistValueDepth(dec, start, 0)
}

func decodePlistValueDepth(dec *xml.Decoder, start xml.StartElement, depth int) (plistValue, error) {
	if start.Name.Space != "" || len(start.Attr) != 0 || depth > maxPlistNesting {
		return plistValue{}, errInvalidServiceDefinition
	}
	switch start.Name.Local {
	case "string", "integer":
		text, err := readPlistText(dec, start)
		return plistValue{kind: start.Name.Local, text: text}, err
	case "true", "false":
		text, err := readPlistText(dec, start)
		if err != nil || strings.TrimSpace(text) != "" {
			return plistValue{}, errInvalidServiceDefinition
		}
		return plistValue{kind: start.Name.Local}, nil
	case "array":
		value := plistValue{kind: "array"}
		for {
			tok, err := dec.Token()
			if err != nil {
				return plistValue{}, err
			}
			switch item := tok.(type) {
			case xml.EndElement:
				if item.Name != start.Name {
					return plistValue{}, errInvalidServiceDefinition
				}
				return value, nil
			case xml.CharData:
				if strings.TrimSpace(string(item)) != "" {
					return plistValue{}, errInvalidServiceDefinition
				}
			case xml.StartElement:
				child, err := decodePlistValueDepth(dec, item, depth+1)
				if err != nil {
					return plistValue{}, err
				}
				value.array = append(value.array, child)
			default:
				return plistValue{}, errInvalidServiceDefinition
			}
		}
	case "dict":
		value := plistValue{kind: "dict", dict: make(map[string]plistValue)}
		for {
			keyStart, err := nextPlistTokenOrEnd(dec, start)
			if err != nil {
				return plistValue{}, err
			}
			if keyStart == nil {
				return value, nil
			}
			if keyStart.Name.Local != "key" || keyStart.Name.Space != "" || len(keyStart.Attr) != 0 {
				return plistValue{}, errInvalidServiceDefinition
			}
			key, err := readPlistText(dec, *keyStart)
			if err != nil || key == "" {
				return plistValue{}, errInvalidServiceDefinition
			}
			if _, duplicate := value.dict[key]; duplicate {
				return plistValue{}, errInvalidServiceDefinition
			}
			valueStart, err := nextPlistStart(dec)
			if err != nil {
				return plistValue{}, errInvalidServiceDefinition
			}
			child, err := decodePlistValueDepth(dec, valueStart, depth+1)
			if err != nil {
				return plistValue{}, err
			}
			value.dict[key] = child
		}
	default:
		return plistValue{}, errInvalidServiceDefinition
	}
}

func nextPlistTokenOrEnd(dec *xml.Decoder, parent xml.StartElement) (*xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch value := tok.(type) {
		case xml.StartElement:
			return &value, nil
		case xml.EndElement:
			if value.Name != parent.Name {
				return nil, errInvalidServiceDefinition
			}
			return nil, nil
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return nil, errInvalidServiceDefinition
			}
		default:
			return nil, errInvalidServiceDefinition
		}
	}
}

func readPlistText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch value := tok.(type) {
		case xml.CharData:
			text.Write(value)
		case xml.EndElement:
			if value.Name != start.Name {
				return "", errInvalidServiceDefinition
			}
			return text.String(), nil
		default:
			return "", errInvalidServiceDefinition
		}
	}
}

func ensurePlistEOF(dec *xml.Decoder) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if text, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(text)) == "" {
			continue
		}
		return errInvalidServiceDefinition
	}
}

func validLaunchdDefinition(values map[string]plistValue) bool {
	if len(values) != 7 || !plistStringIs(values["Label"], serviceLabel) || !plistBoolIs(values["RunAtLoad"], true) || !plistBoolIs(values["AbandonProcessGroup"], true) || !plistStringIs(values["ProcessType"], "Background") || !plistTextIs(values["Umask"], "integer", "63") {
		return false
	}
	keepAlive := values["KeepAlive"]
	if keepAlive.kind != "dict" || len(keepAlive.dict) != 1 || !plistBoolIs(keepAlive.dict["SuccessfulExit"], false) {
		return false
	}
	args := values["ProgramArguments"]
	if args.kind != "array" || len(args.array) != 4 {
		return false
	}
	for _, arg := range args.array {
		if arg.kind != "string" {
			return false
		}
	}
	_, _, err := serviceInvocation([]string{args.array[0].text, args.array[1].text, args.array[2].text, args.array[3].text}, "run")
	return err == nil
}

func plistStringIs(value plistValue, want string) bool { return plistTextIs(value, "string", want) }
func plistBoolIs(value plistValue, want bool) bool {
	if want {
		return value.kind == "true"
	}
	return value.kind == "false"
}
func plistTextIs(value plistValue, kind, want string) bool {
	return value.kind == kind && value.text == want
}

func systemdServiceConfig(data []byte) (string, error) {
	args, err := systemdServiceInvocation(data)
	if err != nil {
		return "", err
	}
	return args[3], nil
}

func systemdServiceInvocation(data []byte) ([]string, error) {
	text := string(data)
	if strings.ContainsRune(text, '\r') {
		return nil, errInvalidServiceDefinition
	}
	sections := make(map[string]map[string]string, 3)
	section := ""
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if strings.TrimSpace(line) != line {
			return nil, errInvalidServiceDefinition
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := line[1 : len(line)-1]
			if name != "Unit" && name != "Service" && name != "Install" {
				return nil, errInvalidServiceDefinition
			}
			if _, duplicate := sections[name]; duplicate {
				return nil, errInvalidServiceDefinition
			}
			sections[name] = make(map[string]string)
			section = name
			continue
		}
		if section == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			return nil, errInvalidServiceDefinition
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			return nil, errInvalidServiceDefinition
		}
		if _, duplicate := sections[section][key]; duplicate {
			return nil, errInvalidServiceDefinition
		}
		sections[section][key] = value
	}
	if len(sections) != 3 || !systemdValuesEqual(sections["Unit"], map[string]string{
		"Description": "Runmoor ephemeral GitHub Actions runner manager",
		"After":       "network-online.target",
	}) || !systemdKeysEqual(sections["Service"], []string{"Type", "ExecStart", "ExecStop", "TimeoutStopSec", "KillMode", "Restart", "RestartSec", "UMask"}) || !systemdValuesEqual(sections["Install"], map[string]string{"WantedBy": "default.target"}) {
		return nil, errInvalidServiceDefinition
	}
	service := sections["Service"]
	if service["Type"] != "simple" || service["TimeoutStopSec"] != "infinity" || service["KillMode"] != "process" || service["Restart"] != "on-failure" || service["RestartSec"] != "5" || service["UMask"] != "0077" {
		return nil, errInvalidServiceDefinition
	}
	startArgs, err := parseSystemdExec(service["ExecStart"])
	if err != nil {
		return nil, errInvalidServiceDefinition
	}
	stopArgs, err := parseSystemdExec(service["ExecStop"])
	if err != nil {
		return nil, errInvalidServiceDefinition
	}
	startBinary, startConfig, err := serviceInvocation(startArgs, "run")
	if err != nil {
		return nil, errInvalidServiceDefinition
	}
	stopBinary, stopConfig, err := serviceInvocation(stopArgs, "stop")
	if err != nil || startBinary != stopBinary || startConfig != stopConfig {
		return nil, errInvalidServiceDefinition
	}
	return startArgs, nil
}

func systemdValuesEqual(actual, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func systemdKeysEqual(actual map[string]string, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := actual[key]; !ok {
			return false
		}
	}
	return true
}

func parseSystemdExec(line string) ([]string, error) {
	var args []string
	var arg strings.Builder
	inQuote, started, closedQuote := false, false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote {
			switch ch {
			case '"':
				inQuote = false
				closedQuote = true
			case '\\', '$', '%':
				if i+1 >= len(line) || line[i+1] != ch {
					if ch == '\\' && i+1 < len(line) && line[i+1] == '"' {
						arg.WriteByte('"')
						i++
						continue
					}
					return nil, errInvalidServiceDefinition
				}
				arg.WriteByte(ch)
				i++
			default:
				arg.WriteByte(ch)
			}
			continue
		}
		if ch == ' ' || ch == '\t' {
			if started {
				args = append(args, arg.String())
				arg.Reset()
				started, closedQuote = false, false
			}
			continue
		}
		if closedQuote {
			return nil, errInvalidServiceDefinition
		}
		if ch == '"' {
			if started {
				return nil, errInvalidServiceDefinition
			}
			inQuote, started = true, true
			continue
		}
		if ch == '\\' || ch == '$' || ch == '%' || ch < 0x20 || ch == 0x7f {
			return nil, errInvalidServiceDefinition
		}
		arg.WriteByte(ch)
		started = true
	}
	if inQuote {
		return nil, errInvalidServiceDefinition
	}
	if started {
		args = append(args, arg.String())
	}
	if len(args) == 0 {
		return nil, errInvalidServiceDefinition
	}
	return args, nil
}

func serviceInvocation(args []string, action string) (string, string, error) {
	if len(args) != 4 || args[1] != action || args[2] != "--config" {
		return "", "", errInvalidServiceDefinition
	}
	binary, err := cleanAbsoluteServicePath(args[0])
	if err != nil || binary != args[0] {
		// Do not clean executable paths before comparison: a symlink component
		// followed by `..` can resolve differently from the cleaned spelling.
		return "", "", errInvalidServiceDefinition
	}
	config, err := cleanAbsoluteServicePath(args[3])
	if err != nil || config != args[3] {
		// A configuration path with dot segments can identify a different file
		// when a preceding component is a symlink. Reject the serialized spelling
		// before normalizing it for the service identity comparison.
		return "", "", errInvalidServiceDefinition
	}
	return binary, config, nil
}
