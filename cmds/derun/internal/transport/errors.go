package transport

import "github.com/delinoio/oss/cmds/derun/internal/errmsg"

func runtimeError(action string, err error, details map[string]any) error {
	return errmsg.Error(errmsg.Runtime(action, err, details), nil)
}

func commandRuntimeError(action string, command []string, workingDir string, err error) error {
	baseDetails := errmsg.CommandDetails(command)
	baseDetails["working_dir"] = workingDir
	return runtimeError(action, err, baseDetails)
}
