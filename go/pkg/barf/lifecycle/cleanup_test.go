package lifecycle

import (
	"context"
	"errors"
	"testing"
)

type fakeImages struct {
	images  []SystemImage
	err     error
	deleted []string
	delErr  error
}

func (f *fakeImages) SystemImages(context.Context) ([]SystemImage, error) {
	return f.images, f.err
}

func (f *fakeImages) DeleteImage(_ context.Context, name string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, name)
	return nil
}

func TestBuildCleanupPlanKeepsRunningAndDefaultBoot(t *testing.T) {
	device := &fakeImages{images: []SystemImage{
		{Name: "running-and-default", DefaultBoot: true, Running: true},
		{Name: "old-1"},
		{Name: "staged-next", DefaultBoot: true},
		{Name: "old-2"},
	}}

	plan, err := BuildCleanupPlan(context.Background(), "sea1-leaf-0", device)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(plan.Delete) != 2 || plan.Delete[0].Name != "old-1" || plan.Delete[1].Name != "old-2" {
		t.Fatalf("delete set = %+v", plan.Delete)
	}
	if len(plan.Keep) != 2 {
		t.Fatalf("keep set = %+v", plan.Keep)
	}
	// Building the plan must not have deleted anything.
	if len(device.deleted) != 0 {
		t.Fatalf("planning deleted images: %v", device.deleted)
	}
}

func TestBuildCleanupPlanRejectsAnEmptyImageList(t *testing.T) {
	// A booted device always has at least the running image, so an empty
	// list means the API answer could not be parsed — not "nothing to
	// keep". Treating it as the latter would delete everything.
	device := &fakeImages{}
	if _, err := BuildCleanupPlan(context.Background(), "sea1-leaf-0", device); err == nil {
		t.Fatal("expected an error for an unparseable image list")
	}
}

func TestExecuteCleanupRefusesWithoutAllowWrites(t *testing.T) {
	device := &fakeImages{images: []SystemImage{
		{Name: "running", Running: true, DefaultBoot: true},
		{Name: "old-1"},
	}}
	plan, err := BuildCleanupPlan(context.Background(), "sea1-leaf-0", device)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	_, err = ExecuteCleanup(context.Background(), plan, device, Options{})
	if !errors.Is(err, ErrWritesNotAllowed) {
		t.Fatalf("expected ErrWritesNotAllowed, got %v", err)
	}
	if len(device.deleted) != 0 {
		t.Fatalf("a dry run deleted images: %v", device.deleted)
	}
}

func TestExecuteCleanupDeletes(t *testing.T) {
	device := &fakeImages{images: []SystemImage{
		{Name: "running", Running: true, DefaultBoot: true},
		{Name: "old-1"},
		{Name: "old-2"},
	}}
	plan, err := BuildCleanupPlan(context.Background(), "sea1-leaf-0", device)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	actions, err := ExecuteCleanup(context.Background(), plan, device, Options{AllowWrites: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("actions = %v", actions)
	}
	if len(device.deleted) != 2 || device.deleted[0] != "old-1" {
		t.Fatalf("deleted = %v", device.deleted)
	}
}

func TestExecuteCleanupStopsOnTheFirstFailure(t *testing.T) {
	device := &fakeImages{
		images: []SystemImage{{Name: "running", Running: true, DefaultBoot: true}, {Name: "old-1"}},
		delErr: errors.New("image in use"),
	}
	plan, _ := BuildCleanupPlan(context.Background(), "sea1-leaf-0", device)
	if _, err := ExecuteCleanup(context.Background(), plan, device, Options{AllowWrites: true}); err == nil {
		t.Fatal("expected the delete failure to surface")
	}
}

// The end-to-end guard: cleanup driven through the real API client with
// writes off must not send a single request to the device.
func TestCleanupThroughTheAPIClientRespectsTheWriteGate(t *testing.T) {
	server := newAPIServer(t)
	server.reply["show"] = "Name                     Default boot    Running\n" +
		"-----------------------  --------------  ---------\n" +
		"new                      yes             yes\n" +
		"old                      no              no\n"

	client := server.client(t, false)
	plan, err := BuildCleanupPlan(context.Background(), "test-device", client)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].Name != "old" {
		t.Fatalf("delete set = %+v", plan.Delete)
	}

	// Even if a caller bypasses the Options gate, the client refuses.
	if _, err := ExecuteCleanup(context.Background(), plan, client, Options{AllowWrites: true}); !errors.Is(err, ErrWritesNotAllowed) {
		t.Fatalf("expected the client's own write gate to refuse, got %v", err)
	}
	for _, request := range server.recorded() {
		if request.Endpoint != "show" {
			t.Fatalf("a non-show request reached the device: %+v", request)
		}
	}
}
