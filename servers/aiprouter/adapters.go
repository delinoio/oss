// SPDX-License-Identifier: Apache-2.0
package main

import (
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/azure"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/commandcode"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/cursor"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/devin"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/dsh"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/google"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/kiro"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/mimofree"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/ollama"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/workbuddy"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/zed"
)

func providerAdapters() map[inference.Wire]inference.Adapter {
	out := map[inference.Wire]inference.Adapter{ollama.Wire: ollama.New(), workbuddy.Wire: workbuddy.New(), commandcode.Wire: commandcode.New(), mimofree.Wire: mimofree.New(), dsh.Wire: dsh.New(), azure.Wire: azure.New(), google.WireVertex: google.NewVertex(google.Options{}), google.WireAPI: google.NewAPI(google.Options{}), google.WireAntigravity: google.NewAntigravity(google.Options{}), google.WireGeminiCLI: google.NewGeminiCLI(google.Options{}), kiro.Wire: kiro.New(), cursor.Wire: cursor.New(), devin.Wire: devin.New()}
	for wire, family := range map[inference.Wire]zed.Provider{zed.WireAnthropic: zed.Anthropic, zed.WireOpenAI: zed.OpenAI, zed.WireGoogle: zed.Google, zed.WireXAI: zed.XAI} {
		a, e := zed.New(family)
		if e == nil {
			out[wire] = a
		}
	}
	return out
}

// Reuse the same adapter instance so request recovery and decoding share policy.
func providerRecoveryPolicies(adapters map[inference.Wire]inference.Adapter) map[inference.Wire]inference.HTTPRecoveryPolicy {
	out := map[inference.Wire]inference.HTTPRecoveryPolicy{devin.Wire: devin.RecoveryPolicy{}}
	for wire, adapter := range adapters {
		if policy, ok := adapter.(inference.HTTPRecoveryPolicy); ok {
			out[wire] = policy
		}
	}
	return out
}
