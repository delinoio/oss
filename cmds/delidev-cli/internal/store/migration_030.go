// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Activate only after the real OAuth schema 29. The explicit identity set keeps
// both this migration and all historical seeds immutable after later additions.
func migration030(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES('provider_presets_layout','hosted-additions-26-v1')"); err != nil {
		return storageError(err)
	}
	return seedProviderSet(ctx, tx, []domain.ProviderPresetID{domain.PresetGemini, domain.PresetGroq, domain.PresetMistral, domain.PresetTogetherAI, domain.PresetFireworksAI, domain.PresetPerplexity, domain.PresetCohere, domain.PresetCerebras, domain.PresetNebius, domain.PresetNovita, domain.PresetDeepInfra, domain.PresetHuggingFace, domain.PresetVenice, domain.PresetScaleway, domain.PresetBaseten, domain.PresetMoonshot, domain.PresetMoonshotCN, domain.PresetMiniMax, domain.PresetMiniMaxCN, domain.PresetSiliconFlow, domain.PresetSiliconFlowCN, domain.PresetQianfan, domain.PresetTencentTokenHub, domain.PresetTencentTokenHubInternational, domain.PresetAlibabaModelStudioInternational, domain.PresetAlibabaModelStudioHongKong})
}
