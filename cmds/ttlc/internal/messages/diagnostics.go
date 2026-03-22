package messages

import (
	"fmt"

	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
)

type DiagnosticID string

const (
	DiagnosticModuleMustStartWithPackage      DiagnosticID = "module_must_start_with_package"
	DiagnosticExpectedPackageName             DiagnosticID = "expected_package_name"
	DiagnosticUnsupportedTopLevelDeclaration  DiagnosticID = "unsupported_top_level_declaration"
	DiagnosticExpectedImportPathString        DiagnosticID = "expected_import_path_string"
	DiagnosticExpectedImportGroupClose        DiagnosticID = "expected_import_group_close"
	DiagnosticExpectedTypeName                DiagnosticID = "expected_type_name"
	DiagnosticExpectedTypeNameAfterQualifier  DiagnosticID = "expected_type_name_after_qualifier"
	DiagnosticOnlyStructTypeSupported         DiagnosticID = "only_struct_type_supported"
	DiagnosticExpectedStructOpenBrace         DiagnosticID = "expected_struct_open_brace"
	DiagnosticExpectedStructFieldName         DiagnosticID = "expected_struct_field_name"
	DiagnosticExpectedStructCloseBrace        DiagnosticID = "expected_struct_close_brace"
	DiagnosticExpectedFuncAfterTask           DiagnosticID = "expected_func_after_task"
	DiagnosticExpectedTaskName                DiagnosticID = "expected_task_name"
	DiagnosticExpectedFunctionName            DiagnosticID = "expected_function_name"
	DiagnosticExpectedParameterListOpen       DiagnosticID = "expected_parameter_list_open"
	DiagnosticExpectedParameterName           DiagnosticID = "expected_parameter_name"
	DiagnosticExpectedParameterListClose      DiagnosticID = "expected_parameter_list_close"
	DiagnosticExpectedGenericArgsClose        DiagnosticID = "expected_generic_args_close"
	DiagnosticExpectedBlockOpen               DiagnosticID = "expected_block_open"
	DiagnosticExpectedBlockClose              DiagnosticID = "expected_block_close"
	DiagnosticTaskReturnTypeRequired          DiagnosticID = "task_return_type_required"
	DiagnosticExpectedExpressionCloseParen    DiagnosticID = "expected_expression_close_paren"
	DiagnosticExpectedExpression              DiagnosticID = "expected_expression"
	DiagnosticExpectedSelectorName            DiagnosticID = "expected_selector_name"
	DiagnosticExpectedCallArgsClose           DiagnosticID = "expected_call_args_close"
	DiagnosticExpectedCompositeFieldName      DiagnosticID = "expected_composite_field_name"
	DiagnosticExpectedCompositeFieldColon     DiagnosticID = "expected_composite_field_colon"
	DiagnosticExpectedCompositeCloseBrace     DiagnosticID = "expected_composite_close_brace"
	DiagnosticInvalidUTF8Rune                 DiagnosticID = "invalid_utf8_rune"
	DiagnosticUnsupportedToken                DiagnosticID = "unsupported_token"
	DiagnosticUnterminatedStringLiteral       DiagnosticID = "unterminated_string_literal"
	DiagnosticUnterminatedBlockComment        DiagnosticID = "unterminated_block_comment"
	DiagnosticUnsupportedImportInSema         DiagnosticID = "unsupported_import_in_sema"
	DiagnosticDuplicateTypeDeclaration        DiagnosticID = "duplicate_type_declaration"
	DiagnosticDuplicateStructFieldDeclaration DiagnosticID = "duplicate_struct_field_declaration"
	DiagnosticDuplicateTaskDeclaration        DiagnosticID = "duplicate_task_declaration"
	DiagnosticDuplicateTaskParameterName      DiagnosticID = "duplicate_task_parameter_name"
	DiagnosticTaskMustReturnVC                DiagnosticID = "task_must_return_vc"
	DiagnosticReadRequiresOneArgument         DiagnosticID = "read_requires_one_argument"
	DiagnosticFunctionTaskNameCollision       DiagnosticID = "function_task_name_collision"
	DiagnosticDuplicateFunctionDeclaration    DiagnosticID = "duplicate_function_declaration"
	DiagnosticDuplicateFunctionParameterName  DiagnosticID = "duplicate_function_parameter_name"
	DiagnosticRunTaskRequired                 DiagnosticID = "run_task_required"
	DiagnosticTaskDependencyCycle             DiagnosticID = "task_dependency_cycle"
	DiagnosticTaskNotFound                    DiagnosticID = "task_not_found"
	DiagnosticImportResolveFailed             DiagnosticID = "import_resolve_failed"
	DiagnosticImportCycle                     DiagnosticID = "import_cycle"
	DiagnosticImportReadFailed                DiagnosticID = "import_read_failed"
	DiagnosticMissingRunArgument              DiagnosticID = "missing_run_argument"
	DiagnosticInvalidRunArgumentType          DiagnosticID = "invalid_run_argument_type"
	DiagnosticUnknownRunArgument              DiagnosticID = "unknown_run_argument"
	DiagnosticInvalidRunArgsJSON              DiagnosticID = "invalid_run_args_json"
	DiagnosticEmitStageFailure                DiagnosticID = "emit_stage_failure"
	DiagnosticCommandFailure                  DiagnosticID = "command_failure"
)

var diagnosticTemplates = map[DiagnosticID]string{
	DiagnosticModuleMustStartWithPackage:      "Invalid module header. Expected a package declaration (for example: package build).",
	DiagnosticExpectedPackageName:             "Invalid package declaration. Expected a package name after the package keyword.",
	DiagnosticUnsupportedTopLevelDeclaration:  "Unsupported top-level declaration. Expected one of: import, type, task, func.",
	DiagnosticExpectedImportPathString:        "Invalid import declaration. Expected a string path (for example: import \"pkg/utils\").",
	DiagnosticExpectedImportGroupClose:        "Invalid import group. Expected ')' to close the import list.",
	DiagnosticExpectedTypeName:                "Invalid type declaration. Expected a type name.",
	DiagnosticExpectedTypeNameAfterQualifier:  "Invalid qualified type reference. Expected a type name after '.'.",
	DiagnosticOnlyStructTypeSupported:         "Unsupported type declaration. Only struct types are currently supported.",
	DiagnosticExpectedStructOpenBrace:         "Invalid struct declaration. Expected '{' after 'struct'.",
	DiagnosticExpectedStructFieldName:         "Invalid struct field declaration. Expected a field name.",
	DiagnosticExpectedStructCloseBrace:        "Invalid struct declaration. Expected '}' to close the struct body.",
	DiagnosticExpectedFuncAfterTask:           "Invalid task declaration. Expected the 'task func' form.",
	DiagnosticExpectedTaskName:                "Invalid task declaration. Expected a task name.",
	DiagnosticExpectedFunctionName:            "Invalid function declaration. Expected a function name.",
	DiagnosticExpectedParameterListOpen:       "Invalid parameter list. Expected '('.",
	DiagnosticExpectedParameterName:           "Invalid parameter declaration. Expected a parameter name.",
	DiagnosticExpectedParameterListClose:      "Invalid parameter list. Expected ')' to close parameters.",
	DiagnosticExpectedGenericArgsClose:        "Invalid generic type arguments. Expected ']'.",
	DiagnosticExpectedBlockOpen:               "Invalid block declaration. Expected '{'.",
	DiagnosticExpectedBlockClose:              "Invalid block declaration. Expected '}' to close the block.",
	DiagnosticTaskReturnTypeRequired:          "Invalid task signature. Expected an explicit return type (for example: Vc[T]).",
	DiagnosticExpectedExpressionCloseParen:    "Invalid expression. Expected ')' to close a parenthesized expression.",
	DiagnosticExpectedExpression:              "Invalid expression. Expected an expression at this position.",
	DiagnosticExpectedSelectorName:            "Invalid selector expression. Expected a field name after '.'.",
	DiagnosticExpectedCallArgsClose:           "Invalid function call. Expected ')' to close call arguments.",
	DiagnosticExpectedCompositeFieldName:      "Invalid composite literal. Expected a field name.",
	DiagnosticExpectedCompositeFieldColon:     "Invalid composite literal field. Expected ':' between field name and value.",
	DiagnosticExpectedCompositeCloseBrace:     "Invalid composite literal. Expected '}' to close the literal.",
	DiagnosticInvalidUTF8Rune:                 "Invalid source text. Found an invalid UTF-8 byte sequence.",
	DiagnosticUnsupportedToken:                "Unsupported token %q. Replace it with a valid TTL token.",
	DiagnosticUnterminatedStringLiteral:       "Unterminated string literal. Add a closing quote.",
	DiagnosticUnterminatedBlockComment:        "Unterminated block comment. Add closing '*/'.",
	DiagnosticUnsupportedImportInSema:         "Unsupported semantic import %q. Imports are parsed but not yet supported in this phase.",
	DiagnosticDuplicateTypeDeclaration:        "Duplicate type declaration %q. Remove or rename one declaration.",
	DiagnosticDuplicateStructFieldDeclaration: "Duplicate struct field declaration: struct %q has multiple fields named %q.",
	DiagnosticDuplicateTaskDeclaration:        "Duplicate task declaration %q. Remove or rename one declaration.",
	DiagnosticDuplicateTaskParameterName:      "Duplicate task parameter: task %q has multiple parameters named %q.",
	DiagnosticTaskMustReturnVC:                "Invalid task return type for %q. Expected Vc[T].",
	DiagnosticReadRequiresOneArgument:         "Invalid read(...) call. Expected exactly 1 argument, got %d.",
	DiagnosticFunctionTaskNameCollision:       "Name collision: function %q conflicts with task %q. Use distinct names.",
	DiagnosticDuplicateFunctionDeclaration:    "Duplicate function declaration %q. Remove or rename one declaration.",
	DiagnosticDuplicateFunctionParameterName:  "Duplicate function parameter: function %q has multiple parameters named %q.",
	DiagnosticRunTaskRequired:                 "Run command requires --task <task-name>.",
	DiagnosticTaskDependencyCycle:             "Task dependency cycle detected: %s. Remove circular read(...) dependencies.",
	DiagnosticTaskNotFound:                    "Task %q was not found in the current module.",
	DiagnosticImportResolveFailed:             "Failed to resolve import %q. Cause: %s.",
	DiagnosticImportCycle:                     "Import cycle detected while loading %q.",
	DiagnosticImportReadFailed:                "Failed to read resolved import %q. Cause: %s.",
	DiagnosticMissingRunArgument:              "Missing required run argument %q.",
	DiagnosticInvalidRunArgumentType:          "Invalid run argument %q at %q. Expected %s, got %s.",
	DiagnosticUnknownRunArgument:              "Unknown run argument %q. Remove it or add a matching task parameter.",
	DiagnosticInvalidRunArgsJSON:              "Invalid --args payload. %s",
	DiagnosticEmitStageFailure:                "Go source emission failed. Cause: %s.",
	DiagnosticCommandFailure:                  "%s command failed. Cause: %s.",
}

func FormatDiagnostic(id DiagnosticID, args ...any) string {
	template, ok := diagnosticTemplates[id]
	if !ok {
		return fmt.Sprintf("Internal diagnostic template %q is not defined.", id)
	}
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

func NewDiagnostic(kind contracts.DiagnosticKind, id DiagnosticID, line int, column int, args ...any) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		Kind:    kind,
		Message: FormatDiagnostic(id, args...),
		Line:    line,
		Column:  column,
	}
}

func NewDiagnosticWithMessage(kind contracts.DiagnosticKind, line int, column int, message string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		Kind:    kind,
		Message: message,
		Line:    line,
		Column:  column,
	}
}
