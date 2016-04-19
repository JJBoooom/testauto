package scp

import (
	"fmt"
	"io/ioutil"
	"log"

	"auto/Godeps/_workspace/src/golang.org/x/crypto/ssh"
)

var (
	TestDir     = "testdir"
	TestFile    = "test"
	TestProgram = "/root/" + TestDir + "/" + TestFile
)

type ScpClient struct {
	ssh.Client
}

func (client *ScpClient) Run(command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		panic("Failed to create session: " + err.Error())
	}
	defer session.Close()

	b, err := session.Output(command)
	if err != nil {
		fmt.Printf("can't exex command for %v\n", err)
		return "", err
	}

	return string(b), nil
}

func (client *ScpClient) Copy(file string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	byteContent, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatal(err)
		return err
	}
	content := string(byteContent)

	go func(content string) {

		w, err := session.StdinPipe()
		if err != nil {
			log.Fatal(err)
			return
		}
		defer func() {
			if w != nil {
				w.Close()
			}
		}()
		fmt.Fprintln(w, "D0755", 0, TestDir) // mkdir
		fmt.Fprintln(w, "C0655", len(content), TestFile)
		fmt.Fprint(w, content)
		fmt.Fprint(w, "\x00") // transfer end with \x00
	}(content)

	if err := session.Run("/usr/bin/scp -tr ./"); err != nil {
		return err
	}

	return nil
}

func NewScpClient(user string, password string, host string) (*ScpClient, error) {

	clientConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
	}
	client, err := ssh.Dial("tcp", host+":22", clientConfig)
	if err != nil {
		return &ScpClient{}, err
	}

	return &ScpClient{*client}, nil

}

/*
func main() {
	client, err := NewScpClient("root", "Cloudsoar123", "192.168.14.74")
	if err != nil {
		panic(err)
	}
	defer func() {
		if client != nil {
			client.Close()
		}
	}()

	err = client.Copy("./test")
	if err != nil {
		panic(err)
	}

}
*/
