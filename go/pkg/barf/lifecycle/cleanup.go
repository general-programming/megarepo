package lifecycle

import (
	"context"
	"fmt"
)

// ImageLister is the read half of cleanup.
type ImageLister interface {
	SystemImages(ctx context.Context) ([]SystemImage, error)
}

// ImageDeleter is the write half. Implementations must refuse when
// writes are not allowed.
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

// BuildCleanupPlan lists the device's system images and works out which
// are safe to remove: everything that is neither the running image nor
// the default boot image. READ ONLY.
//
// Ports VyOSHost._cleanup_old_images' selection half.
func BuildCleanupPlan(ctx context.Context, hostname string, lister ImageLister) (*CleanupPlan, error) {
	images, err := lister.SystemImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: could not list system images: %w", hostname, err)
	}
	if len(images) == 0 {
		// Matching Python: an empty list means the API answered with
		// something we could not parse, not "no images installed" — a
		// booted device always has at least the running one.
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

// ExecuteCleanup deletes the images in the plan. THIS CHANGES A DEVICE:
// it refuses unless opts.AllowWrites is set, and the deleter itself
// refuses a second time (see APIClient.request), so a dry run cannot
// delete an image even through a caller bug.
//
// Returns one human-readable line per deletion.
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
