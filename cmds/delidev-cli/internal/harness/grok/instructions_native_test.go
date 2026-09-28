package grok

import (
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const nativeInstructionFixture = "Original DeliDev instruction fixture.\nPreserve 한글 and <literal> exactly.\n"

func TestManualNativeGrokInstructions(t *testing.T) {
	nativeTextCompletion(t, true, nativeInstructionFixture)
}

func TestManualNativeGrokMaximumInstructions(t *testing.T) {
	nativeTextCompletion(t, true, strings.Repeat("r", domain.MaxAppliedInstructions))
}
