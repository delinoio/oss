package cli

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/presentation"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"strconv"
)

func githubTokenFormCommand(ctx context.Context, c client, args []string) (any, error) {
	f := flags("integration token-form")
	id, access := f.String("id", "", ""), f.String("access", "", "")
	revision := f.Uint64("revision", 0, "")
	open := f.Bool("open", false, "")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	selection := pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_UNSPECIFIED
	switch domain.GitHubTokenAccess(*access) {
	case domain.GitHubSelectedRepositories:
		selection = pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES
	case domain.GitHubPublicRepositories:
		selection = pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PUBLIC_REPOSITORIES
	case domain.GitHubPrivateRepositories:
		selection = pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES
	}
	if domain.ID(*id).Validate() != nil || *revision == 0 || selection == pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_UNSPECIFIED {
		return nil, domain.Fail(domain.InvalidArgument, "Select a profile, current revision and token access.", "Use --id ID --revision N --access selected-repositories|public-repositories|private-repositories.")
	}
	reply, err := c.integrations.GetGitHubTokenForm(ctx, request(c, &pb.GetGitHubTokenFormRequest{ProfileId: *id, ExpectedRevision: *revision, Access: selection}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var value domain.GitHubTokenForm
	if reply.Msg.SchemaVersion != 1 || domain.Decode(reply.Msg.DocumentJson, &value) != nil || value.Validate() != nil || value.ProfileID != domain.ID(*id) || value.ProfileRevision != strconv.FormatUint(*revision, 10) || value.Access != domain.GitHubTokenAccess(*access) {
		return nil, domain.Fail(domain.RecoveryRequired, "The form response differs from the selected profile.", "Refresh the profile and prepare the form again.")
	}
	guidance := "No classic scopes are preselected for public repository lookup. Review the form before generating a token."
	if value.Access == domain.GitHubSelectedRepositories {
		guidance = "The form defaults to All repositories. Select Only select repositories and choose the required repositories under the displayed resource owner. Five read-only permissions are preselected; Checks is not offered by the verified form. Review expiry and organization approval requirements."
	} else if value.Access == domain.GitHubPrivateRepositories {
		guidance = "Classic repo grants broad read/write private-repository access even though DeliDev's GitHub API use is read-only. Prefer fine-grained selected repositories when possible. Review scopes and expiry before generating a token."
	}
	result := map[string]any{"form": value, "guidance": guidance, "dispatched": false}
	if *open {
		if err := presentation.OpenGitHub(ctx, value.URL); err != nil {
			return result, err
		}
		result["dispatched"] = true
	}
	return result, nil
}

func githubPresentationCommand(ctx context.Context, args []string, input io.Reader) (any, error) {
	if len(args) == 0 || (args[0] != "open-github" && args[0] != "dispatch-github") {
		return nil, usage()
	}
	f := flags("presentation " + args[0])
	address := f.String("url", "", "")
	stdin := f.Bool("url-stdin", false, "")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *stdin {
		if *address != "" || args[0] != "open-github" {
			return nil, domain.Fail(domain.InvalidArgument, "Select one presentation input source.", "Use --url-stdin without --url for local GitHub opening.")
		}
		var err error
		*address, err = readGitHubPresentationInput(input)
		if err != nil {
			return nil, err
		}
	}
	if err := domain.ValidateGitHubPresentationURL(*address); err != nil {
		return nil, err
	}
	var err error
	if args[0] == "dispatch-github" {
		err = presentation.DispatchGitHub(*address)
	} else {
		err = presentation.OpenGitHub(ctx, *address)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"dispatched": true}, nil
}

func readGitHubPresentationInput(input io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(input, 2051))
	defer clear(raw)
	if err != nil {
		return "", domain.Fail(domain.InvalidArgument, "The presentation input could not be read.", "Provide only the prepared GitHub address through stdin.")
	}
	if bytes.HasSuffix(raw, []byte("\r\n")) {
		raw = raw[:len(raw)-2]
	} else if bytes.HasSuffix(raw, []byte("\n")) {
		raw = raw[:len(raw)-1]
	}
	value := string(raw)
	if err := domain.ValidateGitHubPresentationURL(value); err != nil {
		return "", err
	}
	return value, nil
}
