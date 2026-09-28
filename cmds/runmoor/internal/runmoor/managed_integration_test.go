package runmoor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestManagedDockerImageIntegration(t *testing.T) {
	if os.Getenv("RUNMOOR_DOCKER_TEST") != "1" {
		t.Skip("opt-in local Docker image preparation")
	}
	c, s := fixtureStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	builder := &ManagedImageBuilder{Store: s, Images: &ImageManager{Store: s}}
	p := c.Pools[0]
	p.Image = ""
	p.Resources = Resources{1, 1024}
	a := RunnerArtifact{ID: newID(), Pool: p.Name, Backend: Docker, Phase: ArtifactPreparing, Reserved: true, Resources: p.Resources, CreatedAt: nowUTC()}
	if err := s.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if v := s.View().Artifacts[a.ID]; v != nil {
			if err := builder.Cleanup(cleanup, c, *v); err != nil {
				t.Error(err)
			}
		}
	})
	official, err := builder.Prepare(ctx, c, p, a, RunnerRelease{Tag: "v2.337.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !immutableDockerImage(official.Image) || official.RunnerVersion != "2.337.0" {
		t.Fatal("official image was not pinned")
	}
	cli, err := dockerClient(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	source, err := cli.ImageInspect(ctx, official.Image)
	if err != nil {
		t.Fatal(err)
	}
	// Replace only the runner in a candidate, using a checksummed fixture archive.
	release, body := fixtureRelease(t, Docker, p.Arch)
	builder.Client = archiveClient(body)
	p.Image = official.Image
	custom := a
	custom.ID = newID()
	if err = s.Update(func(s *Snapshot) error { s.Artifacts[custom.ID] = &custom; return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if v := s.View().Artifacts[custom.ID]; v != nil {
			if err := builder.Cleanup(cleanup, c, *v); err != nil {
				t.Error(err)
			}
		}
	})
	resolved, err := builder.Prepare(ctx, c, p, custom, release)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Image == official.Image || resolved.RunnerVersion != "2.338.0" {
		t.Fatal("custom candidate did not preserve its own identity")
	}
	after, err := cli.ImageInspect(ctx, official.Image)
	if err != nil || source.ID != after.ID {
		t.Fatal("source image changed")
	}
	built, err := cli.ImageInspect(ctx, resolved.Image)
	if err != nil {
		t.Fatal(err)
	}
	if built.Config.Labels[artifactKey] != custom.ID || built.Config.Labels[ownerKey] != s.View().Installation {
		t.Fatal("candidate ownership missing")
	}
	if _, err = cli.ContainerInspect(ctx, artifactName(custom.ID), client.ContainerInspectOptions{}); err == nil {
		t.Fatal("preparation container survived success")
	}
}
