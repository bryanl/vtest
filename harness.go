package vtest

import (
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/digitalocean/godo"
	"github.com/digitalocean/godo/util"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"
)

var (
	// SimultaenousRuns is how many instance tests to run concurrently.
	SimultaenousRuns = 3

	// InstanceImageSlug is the default image for the instances.
	InstanceImageSlug = "ubuntu-16-04-x64"

	// InstanceSize is the default size for the instances.
	InstanceSize = "16gb"

	// VolumeSize is the the default volume size.
	VolumeSize int64 = 100

	// RemoteSSHUser is the remote SSH user.
	RemoteSSHUser = "root"

	// S3BaseURL is the base URL for the S3 upload location.
	S3BaseURL = "https://s3.pifft.com/vtest"

	// DropletWarmupPeriod is a wait period after Droplets are created, so init
	// scripts can run.
	DropletWarmupPeriod = 30 * time.Second
)

// InstanceConfig is the configuration for booting an insstance.
type InstanceConfig struct {
	SSHKeyID   int
	Region     string
	GodoClient *godo.Client
	Name       string

	Err chan<- error
}

// Harness is the benchmark harness for controlling the testing.
type Harness struct {
	Regions    []string
	RunCount   int
	GodoClient *godo.Client
	privateKey []byte
	runID      string
}

// NewHarness creates an instance of Harness.
func NewHarness(token string, regions []string, runCount int) (*Harness, error) {
	godoClient := newGodoClient(token)

	privateKey, err := GenerateSSHKey()
	if err != nil {
		return nil, errors.Wrap(err, "generate ssh key error")
	}

	runID := randString(5)

	return &Harness{
		Regions:    regions,
		RunCount:   runCount,
		GodoClient: godoClient,
		privateKey: privateKey,
		runID:      runID,
	}, nil
}

// Run runs the harness
func (h *Harness) Run() error {
	logger := log.WithField("runID", h.runID)

	logger.Info("creating SSH key")
	signer, err := ssh.ParsePrivateKey(h.privateKey)
	if err != nil {
		return errors.Wrap(err, "parse private key failure")
	}

	pubKey := signer.PublicKey()
	pubKeyPem := ssh.MarshalAuthorizedKey(pubKey)

	keyName := "vtest-" + h.runID
	kcr := &godo.KeyCreateRequest{
		Name:      keyName,
		PublicKey: string(pubKeyPem),
	}

	keyID, err := createSSHKey(h.GodoClient, kcr)
	if err != nil {
		return errors.Wrap(err, "key create falure")
	}

	defer func(id int) {
		h.GodoClient.Keys.DeleteByID(keyID)
	}(keyID)

	errChan := make(chan error)
	ch := make(chan *InstanceConfig)
	var wg sync.WaitGroup

	for i := 0; i < SimultaenousRuns; i++ {
		go func(id int) {
			log.WithField("workerID", id).Info("worker starting up")
			wg.Add(1)
			defer func() {
				wg.Done()
				log.WithField("workerID", id).Info("worker shutting down")
			}()

			for ic := range ch {
				h.processInstance(ic)
			}
		}(i)
	}

	for _, region := range h.Regions {
		x := 0
		for i := 0; i < h.RunCount; i++ {
			x++
			ic := &InstanceConfig{
				SSHKeyID:   keyID,
				Region:     region,
				GodoClient: h.GodoClient,
				Err:        errChan,
				Name:       fmt.Sprintf("vtest-%s-%s-%d", h.runID, region, i),
			}

			ch <- ic
		}
	}

	close(ch)

	go func() {
		for {
			select {
			case err := <-errChan:
				log.WithError(err).Error("harness failure")
			}
		}
	}()

	wg.Wait()

	return nil
}

type processFn func() error

func (h *Harness) processInstance(ic *InstanceConfig) {
	inst, err := NewInstance(ic.GodoClient, ic.Name, ic.Region, ic.SSHKeyID, h.privateKey)
	if err != nil {
		ic.Err <- errors.Wrap(err, "initialize instance failure")
		return
	}

	if err := inst.Boot(); err != nil {
		ic.Err <- errors.Wrap(err, "droplet boot failure")
		return
	}

	defer func() {
		inst.Destroy()
	}()

	if err := inst.Fetch(S3BaseURL+"/bin/run.sh", "/root/run.sh", "755"); err != nil {
		ic.Err <- errors.Wrap(err, "install runner failure")
		return
	}

	if err := inst.RunCmd("/root/run.sh"); err != nil {
		ic.Err <- errors.Wrap(err, "runner failure")
		return
	}

	if err := inst.CopyToS3("/root/"+inst.name+".out", inst.name+".out"); err != nil {
		ic.Err <- errors.Wrap(err, "uplaod results failure")
		return
	}
}

// Instance is a Droplet that will be benchmarked.
type Instance struct {
	godoClient *godo.Client
	droplet    *godo.Droplet
	volume     *godo.Volume
	name       string
	region     string
	keyID      int
	privateKey []byte
}

// NewInstance creates an instance of Instance.
func NewInstance(godoClient *godo.Client, name, region string, keyID int, privateKey []byte) (*Instance, error) {
	return &Instance{
		name:       name,
		godoClient: godoClient,
		region:     region,
		privateKey: privateKey,
		keyID:      keyID,
	}, nil
}

// Boot boots an instance and prepares it for use.
func (i *Instance) Boot() error {
	logger := log.WithFields(log.Fields{
		"instance": i.name,
		"action":   "boot",
	})

	if i.droplet != nil {
		return errors.New("instance has already been booted")
	}

	godoClient := i.godoClient

	vcr := &godo.VolumeCreateRequest{
		Region:        i.region,
		Name:          i.volumeName(),
		SizeGigaBytes: VolumeSize,
	}

	logger.Info("creating volume")
	volume, err := createVolume(godoClient, vcr)
	if err != nil {
		return errors.Wrapf(err, "volume create failure")
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
		SSHKeys: []godo.DropletCreateSSHKey{
			{ID: i.keyID},
		},
	}

	logger.Info("creating droplet")
	droplet, err := createDroplet(godoClient, dcr, true)
	if err != nil {
		return errors.Wrap(err, "droplet create failure")
	}

	logger.WithField("delay", DropletWarmupPeriod).Info("waiting for droplet to warmup")
	time.Sleep(DropletWarmupPeriod)

	i.droplet = droplet
	return nil
}

// RunCmd runs a command on the instance over SSH.
func (i *Instance) RunCmd(remoteCmd ...string) error {
	ip, err := i.droplet.PublicIPv4()
	if err != nil {
		return errors.Wrap(err, "retrieve droplet public IPv4 failure")
	}

	sshClient, err := NewSSH(RemoteSSHUser, ip, 22, i.privateKey)
	if err != nil {
		return errors.Wrap(err, "create ssh client failure")
	}

	cmd := strings.Join(remoteCmd, " ")
	out, err := sshClient.Run(cmd)
	if err != nil {
		log.WithError(err).
			WithField("cmd", cmd).
			Error("command failure")
		fmt.Println(string(out))
	}

	return nil
}

// CopyToS3 copies a file on the
func (i *Instance) CopyToS3(localFilePath, remoteFileName string) error {
	remoteURL := S3BaseURL + "/output/" + remoteFileName
	return i.RunCmd("curl", "-X", "PUT", "-T", localFilePath, remoteURL)
}

func (i *Instance) sshUserHost() (string, error) {
	ip, err := i.droplet.PublicIPv4()
	if err != nil {
		return "", errors.Wrap(err, "extract public IPv4 error")
	}

	return RemoteSSHUser + "@" + ip, nil
}

// Fetch a file from a URL and puts it on the path. localFilePath is the full path name
// including the directory and file name.
func (i *Instance) Fetch(remoteFileURL, localFilePath, mode string) error {
	if err := i.RunCmd("curl", "-o", localFilePath, "-sSL", remoteFileURL); err != nil {
		return errors.Wrap(err, "fetch download failure")
	}

	if err := i.RunCmd("chmod", mode, localFilePath); err != nil {
		return errors.Wrap(err, "fetch chmod failure")
	}

	return nil
}

// Destroy destroys an instance and removes the volumes.
func (i *Instance) Destroy() error {
	logger := log.WithFields(log.Fields{
		"instance": i.name,
		"action":   "destroy",
	})

	logger.Info("deleting droplet")
	_, err := i.godoClient.Droplets.Delete(i.droplet.ID)
	if err != nil {
		return errors.Wrap(err, "delete droplet failure")
	}

	logger.Info("deleting volume")
	_, err = i.godoClient.Storage.DeleteVolume(i.volume.ID)
	if err != nil {
		return errors.Wrap(err, "delete volume failure")
	}

	logger.Info("deleteing key")
	_, err = i.godoClient.Keys.DeleteByID(i.keyID)
	if err != nil {
		return errors.Wrap(err, "delete ssh key failure")
	}

	return nil
}

func (i *Instance) volumeName() string {
	return i.name + "-vol1"
}

func newGodoClient(token string) *godo.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	oauthClient := oauth2.NewClient(oauth2.NoContext, ts)

	return godo.NewClient(oauthClient)
}

func createSSHKey(client *godo.Client, kcr *godo.KeyCreateRequest) (int, error) {
	k, _, err := client.Keys.Create(kcr)
	if err != nil {
		return 0, err
	}

	return k.ID, nil
}

func createVolume(client *godo.Client, vcr *godo.VolumeCreateRequest) (*godo.Volume, error) {
	v, _, err := client.Storage.CreateVolume(vcr)
	if err != nil {
		log.WithError(err).WithField("volumeName", vcr.Name).Error("could not create name")
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
