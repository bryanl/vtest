package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/bryanl/vtest"
	"github.com/kelseyhightower/envconfig"
)

type regionList []string

func init() {
	rand.Seed(time.Now().UnixNano())
}

func (rl regionList) Decode(value string) error {
	rl = strings.Split(value, ",")
	fmt.Printf("ha: %#v\n", rl)
	return nil
}

type specification struct {
	Token      string `envconfig:"digitalocean_token" required:"true"`
	RegionList string `default:"nyc1,sfo2"`
	RunCount   int    `default:"1"`
}

func main() {
	var s specification
	if err := envconfig.Process("vtest", &s); err != nil {
		logrus.WithError(err).Fatal("unable to read configuration")
	}

	regions := strings.Split(s.RegionList, ",")
	harness, err := vtest.NewHarness(s.Token, regions, s.RunCount)
	if err != nil {
		logrus.WithError(err).Fatal("could not create harness")
	}

	logrus.Info("running harness")
	if err := harness.Run(); err != nil {
		logrus.WithError(err).Fatal("could not run harness")
	}
}
