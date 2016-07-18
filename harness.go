package vtest

import (
	"github.com/digitalocean/godo"
	"github.com/digitalocean/godo/util"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	// InstanceImageSlug is the default image for the instances.
	InstanceImageSlug = "ubuntu-16-04-x64"

	// InstanceSize is the default size for the instances.
	InstanceSize = "16gb"

	// VolumeSize is the the default volume size.
	VolumeSize int64 = 100
)

// Harness is the benchmark harness for controlling the testing.
type Harness struct {
}

// Instance is a Droplet that will be benchmarked.
type Instance struct {
	AccessToken string
	droplet     *godo.Droplet
	volume      *godo.Volume
	name        string
	region      string
}

// Boot boots an instance and prepares it for use.
func (i *Instance) Boot() error {
	if i.droplet != nil {
		return errors.New("instance has already been booted")
	}

	godoClient := newGodoClient(i.AccessToken)

	vcr := &godo.VolumeCreateRequest{
		Region:        i.region,
		Name:          i.volumeName(),
		SizeGigaBytes: VolumeSize,
	}

	volume, err := createVolume(godoClient, vcr)
	if err != nil {
		return errors.Wrap(err, "volume create failure")
	}
	i.volume = volume

	dcr := &godo.DropletCreateRequest{
		Name:   i.name,
		Region: i.region,
		Size:   InstanceSize,
		Image: godo.DropletCreateImage{
			Slug: InstanceImageSlug,
		},
		Volumes: []godo.DropletCreateVolume{
			{Name: volume.Name},
		},
	}

	droplet, err := createDroplet(godoClient, dcr, true)
	if err != nil {
		return errors.Wrap(err, "droplet create failure")
	}

	i.droplet = droplet
	return nil
}

// Destroy destroys an instance and removes the volumes.
func (i *Instance) Destroy() error {
	godoClient := newGodoClient(i.AccessToken)

	_, err := godoClient.Droplets.Delete(i.droplet.ID)
	if err != nil {
		return errors.Wrap(err, "delete droplet failure")
	}

	_, err = godoClient.Storage.DeleteVolume(i.volume.ID)
	if err != nil {
		return errors.Wrap(err, "delete volume failure")
	}

	return nil
}

func (i *Instance) volumeName() string {
	return i.name + "-vol-1"
}

func newGodoClient(token string) *godo.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	oauthClient := oauth2.NewClient(oauth2.NoContext, ts)

	return godo.NewClient(oauthClient)
}

func createVolume(client *godo.Client, vcr *godo.VolumeCreateRequest) (*godo.Volume, error) {
	v, _, err := client.Storage.CreateVolume(vcr)
	if err != nil {
		return nil, err
	}

	return v, nil
}

func createDroplet(client *godo.Client, dcr *godo.DropletCreateRequest, wait bool) (*godo.Droplet, error) {
	d, resp, err := client.Droplets.Create(dcr)
	if err != nil {
		return nil, err
	}

	if wait {
		var action *godo.LinkAction
		for _, a := range resp.Links.Actions {
			if a.Rel == "create" {
				action = &a
				break
			}
		}

		if action != nil {
			_ = util.WaitForActive(client, action.HREF)
			doDroplet, _, err := client.Droplets.Get(d.ID)
			if err != nil {
				return nil, err
			}
			d = doDroplet
		}
	}

	return d, nil
}
