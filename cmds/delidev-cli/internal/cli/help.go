// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

const help = `DeliDev 0.1.0 (unreleased)

Usage: delidev [--data-dir PATH] [--server URL --token-stdin] COMMAND

  server start [--foreground] [--listen IP:PORT] [--tls-cert FILE --tls-key FILE]
               [--allowed-origins ORIGIN,ORIGIN]
  service-control install|status|start|stop|remove --kind server|worker [--revision N]
  server service install|status|start|stop|remove [--revision N]
  worker service install|status|start|stop|remove [--worker-dir PATH --revision N]
  server status | overview | stop
  server ensure [--listen IP:PORT] [--allowed-origins ORIGIN,ORIGIN]
  doctor
  connection list
  connection worker-register|worker-inspect|worker-status|worker-start --id UUID
  connection worker-stop --id UUID --generation UUID
  connection pair --id UUID --name NAME --code-stdin
  connection inspect|verify|retry --id UUID
  device create-pairing --type worker|client --name NAME
  device pair --device-dir PATH --code-stdin
  device pair-local --device-dir PATH
  device inspect-local
  --request-id UUID device recover-local --id UUID --revision N
  device inspect --device-dir PATH
  device revoke --id ID --revision N
  worker pair --worker-dir PATH --name NAME --code-stdin
  worker pair-local --worker-dir PATH
  worker inspect --worker-dir PATH
  worker status --worker-dir PATH
  worker stop --worker-dir PATH --generation UUID-V7
  worker start --worker-dir PATH [--detach]
  repository inspect --machine-id ID --path PATH [--preferred-remote NAME] [--wait]
  machine discover --id ID --revision N [--input FILE|-] [--protocol] [--wait]
  account connect --id ID --revision N (--key-stdin | --keyless)
  account disconnect --id ID --revision N
  account login --id ID --revision N --machine-id ID [--device-code]
  account login-progress --id ID --operation-id ID
  account cancel-login --id ID --revision N
  account refresh --id ID --revision N --machine-id ID
  account logout --id ID --revision N --machine-id ID
  account validate --id ID --revision N
  account status --id ID
  browser-profile capabilities | status --id PROFILE | account-status --account-id ACCOUNT
  browser-profile list [--page-size N --page-token TOKEN]
  browser-profile register --id SESSION --revision N --account-id ACCOUNT
  browser-profile confirm-removal --id PROFILE --revision N --deletion-request-id REQUEST
  account list [--provider-id ID] [--account-type api|subscription] [--limit N] [--page-token TOKEN]
  integration create --input FILE|-
  integration edit --id ID --revision N --input FILE|-
  integration replace-token --id ID --revision N --pat-stdin
  integration validate|delete --id ID --revision N
  network profile save --input FILE|- [--id ID --revision N] [--credential-stdin | --clear-credential]
  network profile list|get|delete [--id ID --revision N] [--limit N --page-token TOKEN]
  network select [--id ROUTE_ID --revision N] [--machine-id ID] [--profile-id ID --profile-revision N]
  network status [--machine-id ID]
  network export-metadata --machine-id ID --revision DESIRED_GENERATION
  integration list|get|snapshot [--id ID]
  integration token-form --id ID --revision N --access selected-repositories|public-repositories|private-repositories [--open]
  integration inspect-repository --repository-id ID
  github pr|issue list --repository-id ID [--state open|closed|all] [--page N --page-size N]
  github pr|issue search --repository-id ID --text TERMS [--state open|closed|all] [--page N]
  github pr|issue get|open --repository-id ID --number N
  github pr diff|rules|ci|feedback|reviewers --repository-id ID --number N
  github pr checks|statuses --repository-id ID --number N [--page N --page-size N]
  github pr problems refresh --repository-id ID --number N [--kind feedback|ci|conflict]
  github pr problems list --remote-repository-id N --pull-request-id N [--limit N --page-token TOKEN]
  github pr problems dismiss --id ID --revision N --content-version SHA256
  github pr remediation list --remote-repository-id N --pull-request-id N [--limit N --page-token TOKEN]
  github pr remediation capabilities
  github pr remediation fix --input PATH|-
  github pr remediation resume --id SET_ID --revision N
  provider presets
  provider inventory [--query TEXT] [--enabled-only] [--limit N] [--page-token TOKEN]
  provider create --preset PRESET [--name NAME]
    --name creates an independent custom copy; --preset alone creates the managed preset
  provider discover --account-id ID --revision N
  model search [--query TEXT] [--provider-id ID] [--include-hidden] [--enabled-providers-only] [--limit N] [--page-token TOKEN]
  model native-discover --machine-id ID --revision N --account-id ID --account-revision N [--include-hidden]
  model native-observation --id JOB_ID
  model native-list --id JOB_ID [--limit N] [--page-token TOKEN]
  model native-cancel --id JOB_ID --revision N
  model resolve --selector ID|ALIAS|NATIVE_ID [--provider-id ID]
  session forward start|status|stop|reconcile --session-id ID [--id ID] [--revision N] [--machine-id ID --worker-port N --local-port N]
  session diagnostics --id ID [--execution-id ID] [--page-size 50] [--page-token TOKEN]
  session files roots|list|read --id ID [--repository-id ID] [--path RELATIVE] [--page-token TOKEN]
  session diff --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]
  session review-context --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]
  session review create|edit|delete|submit|list|get --id SESSION [--review-id ID] [--revision N] [--input PATH]
  session pr link --id SESSION --repository-id ID --number N
  session pr list|get|unlink --id SESSION [--association-id ID] [--revision N]
  session terminal create|list|inspect|input|resize|output|reattach|close --id ID
  session create --input FILE|- [--wait]
  session delete --id ID --revision REV --confirm [--wait]
  session deletion --id ID
  session prepare --id ID --revision N [--wait]
  session recover-workspace --id ID --revision N [--cleanup] [--wait]
  session recover-execution --id ID --revision N --execution-id ID [--wait]
  session subagents --id SESSION [--limit N] [--page-token TOKEN]
  session list [--project-id ID] [--include-archived] [--limit N] [--page-token TOKEN]
  session enqueue --id ID --input FILE|-
  session steer --id SESSION --input-id INPUT --revision N --execution-id EXECUTION --turn-id TURN
  session stop|archive|restore|resume --id ID --revision N
  session compact --id ID --revision N
  session context --id ID
  session switch-account --id ID --revision N --account-id ID
  session rename --id ID --revision N --name NAME
  storage preview|create --session-id ID --expected-revision N
  storage cleanup --session-id ID --expected-revision N --preview-job-id JOB --confirm
  storage inspect|restore|delete --session-id ID --expected-revision N --snapshot-id ID [--confirm]
  storage recover --session-id ID --expected-revision N --recovery-job-id JOB
  storage operation --id JOB
  storage cancel --id JOB --expected-revision N
  schedule create --input FILE|- [--local-worker-dir PATH]
  schedule edit --id ID --revision N --input FILE|- [--local-worker-dir PATH]
  schedule list [--project-id ID] [--enabled all|true|false] [--limit N] [--page-token TOKEN]
  schedule get|inspect|next-run --id ID
  schedule pause|resume|delete|run-now --id ID --revision N
  schedule history --id ID [--limit N] [--page-token TOKEN]
  schedule occurrence --id SCHEDULE --occurrence-id OCCURRENCE
  interaction respond --id ID --revision N --input FILE|-
  interaction approve --id ID --revision N --input FILE|-
  session budget get --id ID
  session budget set --id ID --revision N --currency USD --threshold DECIMAL
  session budget remove --id ID --revision N
  usage pricing get --model-id ID
  usage pricing version --id ID
  usage pricing set --model-id ID --model-revision M --revision N --input PATH [--request-id ID]
  usage summary [--from RFC3339] [--until RFC3339] [--granularity day --timezone IANA] [--session-id ID] [--project-id ID | --general-chat] [--account-id ID] [--provider-id ID] [--model-id ID]
  activity list [--session-id ID] [--project-id ID] [--limit N] [--page-token TOKEN]
  search --query TEXT [--session-id ID] [--project-id ID] [--agent-id ID] [--account-id ID]
    [--outcome all|not-started|running|succeeded|failed|stopped] [--archive all|active|archiving|archived]
    [--limit N] [--page-token TOKEN]
  notification preferences | configure --revision N --interactions on|off --terminals on|off
  notification list [--limit N] | inspect|claim --id INBOX_ID
  notification report --id INBOX_ID --claim-id CLAIM_ID --state submitted|denied|failed|uncertain
  inbox list [--session-id ID] [--project-id ID] [--read-state all|read|unread]
    [--source all|interaction|execution-terminal] [--limit N] [--page-token TOKEN]
  inbox get|inspect --id ID
  inbox mark-read|mark-unread --id ID --revision N
  queue list --session-id ID [--limit N] [--page-token TOKEN]
  queue edit --session-id ID --id ID --revision N --input FILE|-
  queue remove --session-id ID --id ID --revision N
  configuration export [--output PATH]
  configuration preview --input PATH|- [--output PATH]
  configuration apply --input PATH|- [--request-id ID]
  backup create [--wait]
  backup creation --id JOB-ID
  backup creations [--limit N] [--page-token TOKEN]
  backup list [--limit N] [--page-token TOKEN]
  backup inspect --id ID
  backup restore --id ID --expected-revision REV --size-bytes BYTES --modified-at TIME --sha256 SHA256 --expected-restore-revision REV --confirm
  backup restore-status --id REQUEST-ID
  backup delete --id ID --expected-revision REV --size-bytes BYTES --modified-at TIME --sha256 SHA256 --confirm
  backup deletion --id JOB-ID
  backup deletions [--limit N] [--page-token TOKEN]
  settings defaults
  KIND list [--limit 50] [--page-token TOKEN] [--project-id ID] [--session-id ID]
  KIND get --id ID
  KIND snapshot [--session-id ID] [--project-id ID]
  KIND create --input FILE|- [--request-id UUID-V7]
  KIND edit --id ID --revision N --input FILE|- [--request-id UUID-V7]
  KIND delete --id ID --revision N [--request-id UUID-V7]
  agent routing --id ID [--project-id ID]
  events --cursor TOKEN [--session-id ID]
  version

Configuration kinds: project, repository, agent, account, provider, model,
                     template, settings.
Product output is versioned JSON; --json is accepted explicitly.
Progress and structured diagnostics go to stderr. Ordinary commands never start
servers. Retain each mutation's request ID and reuse it only for an exact retry.
Secrets are accepted through stdin, never command arguments.
`

func terminalInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
