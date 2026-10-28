// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type artifactAdapter struct {
	service *artifacts.Service
	auth    *inferenceAdapter
}

func artifactOwner(p inference.Principal) artifacts.Owner {
	return artifacts.Owner{OrganizationID: p.OrganizationID, UserID: p.UserID, Generation: p.MembershipGeneration}
}
func (a artifactAdapter) Put(ctx context.Context, p inference.Principal, data []byte, contentType string) (string, error) {
	if e := a.auth.BeforeSend(ctx, p); e != nil {
		return "", e
	}
	items, e := a.service.StoreResponse(ctx, artifactOwner(p), []artifacts.Image{{Data: data, ContentType: contentType}})
	if e != nil {
		return "", e
	}
	return a.URL(ctx, p, items[0].ID)
}
func (a artifactAdapter) URL(ctx context.Context, p inference.Principal, id string) (string, error) {
	if e := a.auth.BeforeSend(ctx, p); e != nil {
		return "", e
	}
	return a.service.SignedURL(ctx, artifactOwner(p), id)
}
func (a artifactAdapter) PutBatch(ctx context.Context, p inference.Principal, data []inference.GeneratedImage) ([]string, error) {
	if e := a.auth.BeforeSend(ctx, p); e != nil {
		return nil, e
	}
	images := make([]artifacts.Image, len(data))
	for i, item := range data {
		images[i] = artifacts.Image{Data: item.Data, ContentType: item.ContentType}
	}
	items, e := a.service.StoreResponse(ctx, artifactOwner(p), images)
	if e != nil {
		return nil, e
	}
	urls := make([]string, len(items))
	for i, item := range items {
		urls[i], e = a.URL(ctx, p, item.ID)
		if e != nil {
			return nil, e
		}
	}
	return urls, nil
}

var _ inference.ArtifactBatchStore = artifactAdapter{}
