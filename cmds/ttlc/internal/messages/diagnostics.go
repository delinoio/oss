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
	DiagnosticModuleMustStartWithPackage:      "Module must start with a package declaration, for example: package build.",
	DiagnosticExpectedPackageName:             "Package declaration is missing a package name.",
	DiagnosticUnsupportedTopLevelDeclaration:  "Unsupported top-level declaration. Use only import, type, task, or func declarations.",
	DiagnosticExpectedImportPathString:        "Import declaration requires a string path, for example: import \"pkg/utils\".",
	DiagnosticExpectedImportGroupClose:        "Import group is not closed. Add ')' after the import paths.",
	DiagnosticExpectedTypeName:                "Type declaration is missing a type name.",
	DiagnosticExpectedTypeNameAfterQualifier:  "Qualified type reference is missing a type name after '.'.",
	DiagnosticOnlyStructTypeSupported:         "Only struct type declarations are currently supported.",
	DiagnosticExpectedStructOpenBrace:         "Struct declaration is missing '{' after 'struct'.",
	DiagnosticExpectedStructFieldName:         "Struct field declaration is missing a field name.",
	DiagnosticExpectedStructCloseBrace:        "Struct declaration is not closed. Add '}' after struct fields.",
	DiagnosticExpectedFuncAfterTask:           "Task declaration must use the 'task func' form.",
	DiagnosticExpectedTaskName:                "Task declaration is missing a task name.",
	DiagnosticExpectedFunctionName:            "Function declaration is missing a function name.",
	DiagnosticExpectedParameterListOpen:       "Parameter list must start with '('.",
	DiagnosticExpectedParameterName:           "Parameter declaration is missing a parameter name.",
	DiagnosticExpectedParameterListClose:      "Parameter list is not closed. Add ')' after parameters.",
	DiagnosticExpectedGenericArgsClose:        "Generic type arguments are not closed. Add ']'.",
	DiagnosticExpectedBlockOpen:               "Code block must start with '{'.",
	DiagnosticExpectedBlockClose:              "Code block is not closed. Add '}'.",
	DiagnosticTaskReturnTypeRequired:          "Task function must declare a return type, for example Vc[T].",
	DiagnosticExpectedExpressionCloseParen:    "Parenthesized expression is not closed. Add ')'.",
	DiagnosticExpectedExpression:              "Expected an expression at this position.",
	DiagnosticExpectedSelectorName:            "Selector expression is missing a field name after '.'.",
	DiagnosticExpectedCallArgsClose:           "Function call arguments are not closed. Add ')'.",
	DiagnosticExpectedCompositeFieldName:      "Composite literal field is missing a field name.",
	DiagnosticExpectedCompositeFieldColon:     "Composite literal field must use ':' between field name and value.",
	DiagnosticExpectedCompositeCloseBrace:     "Composite literal is not closed. Add '}'.",
	DiagnosticInvalidUTF8Rune:                 "Invalid UTF-8 byte sequence was found in source text.",
	DiagnosticUnsupportedToken:                "Unsupported token %q. Replace it with a valid TTL token.",
	DiagnosticUnterminatedStringLiteral:       "String literal is not terminated. Add a closing quote.",
	DiagnosticUnterminatedBlockComment:        "Block comment is not terminated. Add closing '*/'.",
	DiagnosticUnsupportedImportInSema:         "Import %q is parsed but not supported in this semantic phase.",
	DiagnosticDuplicateTypeDeclaration:        "Type %q is declared more than once. Remove or rename duplicates.",
	DiagnosticDuplicateStructFieldDeclaration: "Struct %q declares field %q more than once.",
	DiagnosticDuplicateTaskDeclaration:        "Task %q is declared more than once. Remove or rename duplicates.",
	DiagnosticDuplicateTaskParameterName:      "Task %q declares parameter %q more than once.",
	DiagnosticTaskMustReturnVC:                "Task %q must return Vc[T].",
	DiagnosticReadRequiresOneArgument:         "read(...) requires exactly one argument.",
	DiagnosticFunctionTaskNameCollision:       "Function %q conflicts with task %q. Use distinct names.",
	DiagnosticDuplicateFunctionDeclaration:    "Function %q is declared more than once. Remove or rename duplicates.",
	DiagnosticDuplicateFunctionParameterName:  "Function %q declares parameter %q more than once.",
	DiagnosticRunTaskRequired:                 "Run command requires --task <task-name>.",
	DiagnosticTaskDependencyCycle:             "Task dependency cycle detected: %s. Remove circular read(...) dependencies.",
	DiagnosticTaskNotFound:                    "Task %q was not found in the current module.",
	DiagnosticImportResolveFailed:             "Import %q could not be resolved. %s",
	DiagnosticImportCycle:                     "Import cycle detected while loading %q.",
	DiagnosticImportReadFailed:                "Import %q was resolved but could not be read. %s",
	DiagnosticMissingRunArgument:              "Missing required run argument %q.",
	DiagnosticInvalidRunArgumentType:          "Invalid type for run argument %q: expected %s.",
	DiagnosticUnknownRunArgument:              "Unknown run argument %q. Remove it or add a matching task parameter.",
	DiagnosticInvalidRunArgsJSON:              "Invalid --args value. %s",
	DiagnosticEmitStageFailure:                "Go source emission failed. %s",
	DiagnosticCommandFailure:                  "%s command failed: %s",
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
