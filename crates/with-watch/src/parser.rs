use crate::error::{Result, WithWatchError};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedShellExpression {
    pub expression: String,
    pub commands: Vec<ShellCommand>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ShellCommand {
    pub env_assignments: Vec<ShellEnvAssignment>,
    pub argv: Vec<String>,
    pub redirects: Vec<ShellRedirect>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ShellEnvAssignment {
    pub key: String,
    pub value: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ShellRedirect {
    pub operator: ShellRedirectOperator,
    pub target: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ShellRedirectOperator {
    Read,
    ReadWrite,
    Write,
    Append,
    WriteAll,
    AppendAll,
    Clobber,
    Other(String),
}

impl ShellRedirectOperator {
    pub fn as_str(&self) -> &str {
        match self {
            Self::Read => "<",
            Self::ReadWrite => "<>",
            Self::Write => ">",
            Self::Append => ">>",
            Self::WriteAll => "&>",
            Self::AppendAll => "&>>",
            Self::Clobber => ">|",
            Self::Other(operator) => operator.as_str(),
        }
    }

    pub fn reads_input(&self) -> bool {
        matches!(self, Self::Read | Self::ReadWrite)
    }

    pub fn writes_output(&self) -> bool {
        matches!(
            self,
            Self::Write | Self::Append | Self::WriteAll | Self::AppendAll | Self::Clobber
        )
    }
}

pub fn parse_shell_expression(expression: &str) -> Result<ParsedShellExpression> {
    let parsed = starbase_args::parse(expression).map_err(|error| WithWatchError::ShellParse {
        message: error.to_string(),
    })?;

    let mut commands = Vec::new();
    for pipeline in parsed.0 {
        collect_pipeline_commands(pipeline, &mut commands)?;
    }

    Ok(ParsedShellExpression {
        expression: expression.to_string(),
        commands,
    })
}

fn collect_pipeline_commands(
    pipeline: starbase_args::Pipeline,
    commands: &mut Vec<ShellCommand>,
) -> Result<()> {
    match pipeline {
        starbase_args::Pipeline::Start(command_list)
        | starbase_args::Pipeline::StartNegated(command_list)
        | starbase_args::Pipeline::Pipe(command_list)
        | starbase_args::Pipeline::PipeAll(command_list)
        | starbase_args::Pipeline::PipeWith(command_list, _) => {
            collect_command_list_commands(command_list, commands)
        }
    }
}

fn collect_command_list_commands(
    command_list: starbase_args::CommandList,
    commands: &mut Vec<ShellCommand>,
) -> Result<()> {
    let mut current_command_index: Option<usize> = None;

    for sequence in command_list.0 {
        match sequence {
            starbase_args::Sequence::Start(command)
            | starbase_args::Sequence::Then(command)
            | starbase_args::Sequence::AndThen(command)
            | starbase_args::Sequence::OrElse(command)
            | starbase_args::Sequence::Passthrough(command) => {
                let shell_command = build_shell_command(command)?;
                if !shell_command.argv.is_empty() {
                    commands.push(shell_command);
                    current_command_index = Some(commands.len() - 1);
                }
            }
            starbase_args::Sequence::Redirect(command, operator) => {
                if let Some(index) = current_command_index {
                    collect_redirects(command, operator, &mut commands[index].redirects);
                }
            }
            starbase_args::Sequence::Stop(_) => {}
        }
    }

    Ok(())
}

fn build_shell_command(command: starbase_args::Command) -> Result<ShellCommand> {
    let mut shell_command = ShellCommand::default();

    for argument in command.0 {
        match argument {
            starbase_args::Argument::EnvVar(key, value, _) => {
                shell_command.env_assignments.push(ShellEnvAssignment {
                    key,
                    value: value.as_str().to_string(),
                });
            }
            starbase_args::Argument::FlagGroup(flag) | starbase_args::Argument::Flag(flag) => {
                shell_command.argv.push(flag);
            }
            starbase_args::Argument::Option(option, Some(value)) => {
                shell_command
                    .argv
                    .push(format!("{option}={}", value.as_str()));
            }
            starbase_args::Argument::Option(option, None) => {
                shell_command.argv.push(option);
            }
            starbase_args::Argument::Value(value) => {
                if shell_command.argv.is_empty() {
                    validate_command_name(value.as_str())?;
                }
                shell_command.argv.push(value.as_str().to_string());
            }
        }
    }

    Ok(shell_command)
}

fn collect_redirects(
    command: starbase_args::Command,
    operator: String,
    redirects: &mut Vec<ShellRedirect>,
) {
    for argument in command.0 {
        match argument {
            starbase_args::Argument::Option(_, _)
            | starbase_args::Argument::Flag(_)
            | starbase_args::Argument::FlagGroup(_)
            | starbase_args::Argument::EnvVar(_, _, _) => {}
            starbase_args::Argument::Value(value) => redirects.push(ShellRedirect {
                operator: classify_redirect_operator(&operator),
                target: value.as_str().to_string(),
            }),
        }
    }
}

fn classify_redirect_operator(operator: &str) -> ShellRedirectOperator {
    match operator {
        "<" => ShellRedirectOperator::Read,
        "<>" => ShellRedirectOperator::ReadWrite,
        ">" => ShellRedirectOperator::Write,
        ">>" => ShellRedirectOperator::Append,
        "&>" | "1&>" | "2&>" => ShellRedirectOperator::WriteAll,
        "&>>" | "1&>>" | "2&>>" => ShellRedirectOperator::AppendAll,
        ">|" => ShellRedirectOperator::Clobber,
        other => ShellRedirectOperator::Other(other.to_string()),
    }
}

fn validate_command_name(command_name: &str) -> Result<()> {
    let lowered = command_name.trim().to_ascii_lowercase();
    let unsupported = matches!(
        lowered.as_str(),
        "if" | "then"
            | "else"
            | "elif"
            | "fi"
            | "for"
            | "while"
            | "until"
            | "do"
            | "done"
            | "case"
            | "esac"
            | "function"
            | "{"
            | "}"
    );

    if unsupported {
        return Err(WithWatchError::UnsupportedShellConstruct {
            construct: command_name.to_string(),
        });
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{parse_shell_expression, ShellRedirectOperator};

    #[test]
    fn parses_command_lines_with_and_or_and_pipeline_operators() {
        let parsed = parse_shell_expression("cp src.txt dest.txt && cat dest.txt | grep hello")
            .expect("parse shell");

        assert_eq!(parsed.commands.len(), 3);
        assert_eq!(parsed.commands[0].argv, vec!["cp", "src.txt", "dest.txt"]);
        assert_eq!(parsed.commands[1].argv, vec!["cat", "dest.txt"]);
        assert_eq!(parsed.commands[2].argv, vec!["grep", "hello"]);
    }

    #[test]
    fn preserves_redirect_targets_as_structured_metadata() {
        let parsed =
            parse_shell_expression("grep hello < input.txt > output.txt").expect("parse shell");

        assert_eq!(parsed.commands.len(), 1);
        let command = &parsed.commands[0];
        assert_eq!(command.argv, vec!["grep", "hello"]);
        assert_eq!(command.redirects.len(), 2);
        assert_eq!(command.redirects[0].operator, ShellRedirectOperator::Read);
        assert_eq!(command.redirects[0].target, "input.txt");
        assert_eq!(command.redirects[1].operator, ShellRedirectOperator::Write);
        assert_eq!(command.redirects[1].target, "output.txt");
    }

    #[test]
    fn rejects_shell_control_flow_keywords() {
        let error =
            parse_shell_expression("if true; then echo hi; fi").expect_err("expected error");
        assert!(error
            .to_string()
            .contains("Shell control-flow is out of scope"));
    }
}
