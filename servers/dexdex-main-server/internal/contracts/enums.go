package contracts

type DeploymentMode string

const (
	DeploymentModeSingleInstance DeploymentMode = "SINGLE_INSTANCE"
	DeploymentModeScale          DeploymentMode = "SCALE"
)

func ParseDeploymentMode(value string) DeploymentMode {
	switch value {
	case string(DeploymentModeScale):
		return DeploymentModeScale
	default:
		return DeploymentModeSingleInstance
	}
}
