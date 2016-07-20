package vtest

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"strconv"

	"github.com/pkg/errors"

	"golang.org/x/crypto/ssh"
)

// SSH is a SSH client.
type SSH struct {
	user       string
	host       string
	port       int
	privateKey []byte
}

// NewSSH creates an instance of SSH.
func NewSSH(user, host string, port int, privateKey []byte) (*SSH, error) {
	return &SSH{
		user:       user,
		host:       host,
		port:       port,
		privateKey: privateKey,
	}, nil
}

// Run command on the remote host.
func (s *SSH) Run(command string) (outStr string, err error) {
	outChan, doneChan, err := s.stream(command)
	if err != nil {
		return outStr, err
	}

	done := false
	for !done {
		select {
		case <-doneChan:
			done = true
		case line := <-outChan:
			// TODO ew.. this is nasty
			outStr += line + "\n"

		}
	}

	return outStr, err
}

func (s *SSH) connect() (*ssh.Session, error) {
	auths := []ssh.AuthMethod{}

	pubKey, err := loadKey(s.privateKey)
	if err != nil {
		return nil, err
	}

	auths = append(auths, ssh.PublicKeys(pubKey))

	config := &ssh.ClientConfig{
		User: s.user,
		Auth: auths,
	}

	portStr := strconv.Itoa(s.port)

	client, err := ssh.Dial("tcp", s.host+":"+portStr, config)
	if err != nil {
		return nil, errors.Wrap(err, "ssh dial failure")
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, errors.Wrap(err, "create new ssh session failure")
	}

	return session, nil
}

func (s *SSH) stream(command string) (output chan string, done chan bool, err error) {
	session, err := s.connect()
	if err != nil {
		return output, done, err
	}

	outReader, err := session.StdoutPipe()
	if err != nil {
		return output, done, err
	}

	errReader, err := session.StderrPipe()
	if err != nil {
		return output, done, err
	}

	outputReader := io.MultiReader(outReader, errReader)
	err = session.Start(command)
	scanner := bufio.NewScanner(outputReader)

	outputChan := make(chan string)
	done = make(chan bool)
	go func(scanner *bufio.Scanner, out chan string, done chan bool) {
		defer close(outputChan)
		defer close(done)
		for scanner.Scan() {
			outputChan <- scanner.Text()
		}

		done <- true
		session.Close()
	}(scanner, outputChan, done)

	return outputChan, done, err
}

func loadKey(in []byte) (ssh.Signer, error) {
	privKey, err := ssh.ParsePrivateKey(in)
	if err != nil {
		return nil, errors.Wrap(err, "parse private key failure")
	}

	return privKey, nil
}

// GenerateSSHKey generates a new SSH key.
func GenerateSSHKey() ([]byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	b := pem.EncodeToMemory(privateKeyPEM)
	_, err = buf.Write(b)
	if err != nil {
		return nil, errors.Wrap(err, "private key write failure")
	}

	return buf.Bytes(), nil
}
