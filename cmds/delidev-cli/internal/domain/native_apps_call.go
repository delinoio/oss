// SPDX-License-Identifier: Apache-2.0
package domain

// NativeAppCallProof is protected original Worker provenance. It is retained
// with the native question and never projected as product installation or
// account authority. The first response claim revalidates this exact frozen
// selection atomically with revocation before admitting the original release.
type NativeAppCallProof struct {
	Scope        NativeAppScope            `json:"scope"`
	Selection    SessionNativeAppSelection `json:"selection"`
	Inventory    NativeAppInventory        `json:"inventory"`
	NativeItemID string                    `json:"native_item_id"`
	AppID        string                    `json:"app_id"`
}

func (p NativeAppCallProof) Validate() error {
	if p.Scope != p.Selection.Scope || p.Scope != p.Inventory.Scope || Text(p.NativeItemID, "original native App call", 1024, true) != nil {
		return NativeAppsUnavailable()
	}
	return AdmitNativeAppCall(p.Selection, p.Selection, p.Inventory, p.AppID)
}
