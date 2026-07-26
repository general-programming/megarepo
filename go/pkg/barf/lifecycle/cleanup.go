package lifecycle

import (
	"context"
	"fmt"
)

// ImageLister is the read half of cleanup.
type ImageLister interface {
	SystemImages(ctx context.Context) ([]SystemImage, error)
}

// ImageDeleter is the write half; it must refuse when writes are not allowed.
type ImageDeleter interface {
	DeleteImage(ctx context.Context, name string) error
}

// CleanupPlan is what `barf device cleanup` would remove.
type CleanupPlan struct {
	Hostname string
	// Delete are the images that are neither running nor default boot.
	Delete []SystemImage
	// Keep are the images that stay, with why.
	Keep []SystemImage
}

// Empty reports whether there is nothing to do.
func (p *CleanupPlan) Empty() bool { return len(p.Delete) == 0 }

// BuildCleanupPlan picks the images safe to remove: everything that is neither
// running nor default boot. READ ONLY.
func BuildCleanupPlan(ctx context.Context, hostname string, lister ImageLister) (*CleanupPlan, error) {
	images, err := lister.SystemImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: could not list system images: %w", hostname, err)
	}
	if len(images) == 0 {
		// A booted device always has at least the running image, so an empty
		// list means an unparseable API answer.
		return nil, fmt.Errorf("%s: could not list system images", hostname)
	}

	plan := &CleanupPlan{Hostname: hostname}
	for _, image := range images {
		if image.DefaultBoot || image.Running {
			plan.Keep = append(plan.Keep, image)
			continue
		}
		plan.Delete = append(plan.Delete, image)
	}
	return plan, nil
}

// ExecuteCleanup deletes the plan's images, one returned line each. THIS
// CHANGES A DEVICE: it refuses unless opts.AllowWrites is set, and the deleter
// refuses again (see APIClient.request).
func ExecuteCleanup(ctx context.Context, plan *CleanupPlan, deleter ImageDeleter, opts Options) ([]string, error) {
	if plan == nil {
		return nil, fmt.Errorf("lifecycle: ExecuteCleanup needs a plan")
	}
	if !opts.AllowWrites {
		return nil, ErrWritesNotAllowed
	}

	var actions []string
	for _, image := range plan.Delete {
		if err := deleter.DeleteImage(ctx, image.Name); err != nil {
			return actions, fmt.Errorf("%s: deleting image %s: %w", plan.Hostname, image.Name, err)
		}
		actions = append(actions, "deleted image "+image.Name)
	}
	return actions, nil
}
