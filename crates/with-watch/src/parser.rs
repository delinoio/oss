use crate::error::{Result, WithWatchError};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedShellExpression {
    pub expression: String,
    pub input_candidates: Vec<String>,
}

pub fn parse_shell_expression(expression: &str) -> Result<ParsedShellExpression> {
    let parsed = starbase_args::parse(expression).map_err(|error| WithWatchError::ShellParse {
        message: error.to_string(),
    })?;

    let mut input_candidates = Vec::new();
    for pipeline in parsed.0 {
        collect_pipeline_inputs(pipeline, &mut input_candidates)?;
    }

    Ok(ParsedShellExpression {
        expression: expression.to_string(),
        input_candidates,
    })
}

fn collect_pipeline_inputs(
    pipeline: starbase_args::Pipeline,
    input_candidates: &mut Vec<String>,
) -> Result<()> {
    match pipeline {
        starbase_args::Pipeline::Start(command_list)
        | starbase_args::Pipeline::StartNegated(command_list)
        | starbase_args::Pipeline::Pipe(command_list)
        | starbase_args::Pipeline::PipeAll(command_list)
        | starbase_args::Pipeline::PipeWith(command_list, _) => {
            collect_command_list_inputs(command_list, input_candidates)
        }
    }
}

fn collect_command_list_inputs(
    command_list: starbase_args::CommandList,
    input_candidates: &mut Vec<String>,
) -> Result<()> {
    for sequence in command_list.0 {
        match sequence {
            starbase_args::Sequence::Start(command)
            | starbase_args::Sequence::Then(command)
            | starbase_args::Sequence::AndThen(command)
            | starbase_args::Sequence::OrElse(command)
            | starbase_args::Sequence::Passthrough(command)
            | starbase_args::Sequence::Redirect(command, _) => {
                collect_command_inputs(command, input_candidates)?;
            }
            starbase_args::Sequence::Stop(_) => {}
        }
    }

    Ok(())
}

fn collect_command_inputs(
    command: starbase_args::Command,
    input_candidates: &mut Vec<String>,
) -> Result<()> {
    let mut command_name_seen = false;

    for argument in command.0 {
        match argument {
            starbase_args::Argument::EnvVar(_, value, _) => {
                input_candidates.push(value.as_str().to_string());
            }
            starbase_args::Argument::Flag(_) | starbase_args::Argument::FlagGroup(_) => {}
            starbase_args::Argument::Option(_, Some(value)) => {
                input_candidates.push(value.as_str().to_string());
            }
            starbase_args::Argument::Option(_, None) => {}
            starbase_args::Argument::Value(value) => {
                if !command_name_seen {
                    validate_command_name(value.as_str())?;
                    command_name_seen = true;
                    continue;
                }

                input_candidates.push(value.as_str().to_string());
            }
        }
    }

    Ok(())
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
    use super::parse_shell_expression;

    #[test]
    fn parses_command_lines_with_and_or_and_pipeline_operators() {
        let parsed = parse_shell_expression("cp src.txt dest.txt && cat dest.txt | grep hello")
            .expect("parse shell");

        assert!(parsed.input_candidates.contains(&"src.txt".to_string()));
        assert!(parsed.input_candidates.contains(&"dest.txt".to_string()));
        assert!(parsed.input_candidates.contains(&"hello".to_string()));
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
