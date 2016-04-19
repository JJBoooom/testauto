package main

import (
	"auto/scp"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
)

var (
	CaseFile   = "./cases.json"
	resultFile = "./test.result"
	caseResult = "./case.result"
	logDir     = "./log"
	log        = logrus.New()
	ResultChan = make(chan interface{})
	DoneChan   = make(chan int)
	Record     = make(map[CaseStruct]Result)
	HostFile   = "./testhost"

	TestListenPort      string
	RegistryIp          string
	RegistryPort        string
	ServerIp            string
	ServerPort          string
	LogicProgram        string
	DockerLoginUser     string
	DockerLoginPassword string
	TestProgram         = "./test"

	TestHost = []string{}
)

//记录每个case运行的结果
type Result struct {
	Nsuccess      int            //成功次数
	Nfail         int            //失败次数
	Reason        map[string]int //失败原因以及统计
	pullPushCount []string       //镜像推/拉统计
}

//test cases
type Cases struct {
	Case []CaseStruct `json:"cases"`
}

//case描述
type CaseStruct struct {
	NumTest      int `json:"tests"`     //测试次数
	NumPullLimit int `json:"pulllimit"` //pull镜像上限, 0 - 不限制
	NumPushLimit int `json:"pushlimit"` //push镜像上限, 0 - 不限制
}

//自定义格式工具,目的是利用logrus日志模块写入文件的功能
type MyFormatter struct {
}

func (f *MyFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	b := &bytes.Buffer{}
	b.WriteString(entry.Message)
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func init() {
	log.Level = logrus.DebugLevel
	log.Formatter = &logrus.TextFormatter{DisableColors: true}

	flag.StringVar(&TestListenPort, "testPort", "", "test program listen port")
	flag.StringVar(&RegistryIp, "rip", "", "registry Ip")
	flag.StringVar(&RegistryPort, "rport", "", "registry port")
	flag.StringVar(&ServerIp, "sip", "", "server ip")
	flag.StringVar(&ServerPort, "sport", "", "ServerPort")
	flag.StringVar(&LogicProgram, "logic", "./logic", "location of logic program")
	flag.StringVar(&DockerLoginUser, "user", "", "user name")
	flag.StringVar(&DockerLoginPassword, "password", "", "user password")
	flag.Parse()

	if TestListenPort == "" || RegistryIp == "" {
		fmt.Printf("invalid arguments")
		os.Exit(1)
	}

	if RegistryPort == "" || ServerIp == "" || ServerPort == "" {
		fmt.Printf("invalid arguments")
		os.Exit(1)
	}

	if LogicProgram == "" || DockerLoginUser == "" || DockerLoginPassword == "" {
		fmt.Printf("invalid arguments")
		os.Exit(1)
	}
}

//准备测试环境
func Prepare() {
	//读取文件,确认测试主机地址
	byteContent, err := ioutil.ReadFile(HostFile)
	if err != nil {
		panic(err)
	}
	content := string(byteContent)

	content = strings.TrimSpace(content)
	host := strings.Split(content, "\n")
	if len(host) == 0 {
		//panic("doesn't set test host")
		fmt.Println("doesn't set test host")
		os.Exit(1)
	}

	//将测试程序远程拷贝到测试主机
	countChan := make(chan int)
	for _, j := range host {
		TestHost = append(TestHost, j)
		go func(host string) {
			fmt.Println(host)
			client, err := scp.NewScpClient("root", "Cloudsoar123", host)

			if err != nil {
				panic(host + " scp fail " + err.Error())
			}
			defer func() {
				if client != nil {
					client.Close()
				}

			}()
			err = client.Copy(TestProgram)
			if err != nil {
				panic(host + " scp fail " + err.Error())
			}
			countChan <- 1
		}(j)
	}
	var x int
	for {
		x += <-countChan
		if x == len(host) {
			break
		}
	}
}

//从pull/push.result文件中提取镜像推拉统计
func getResult(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}

	defer f.Close()
	f.Seek(-30, 2)

	b := make([]byte, 50)
	_, err = f.Read(b)
	if err != nil {
		if err != io.EOF {
			return "", errors.New("does not reach the end of result file")
		}
	}
	ret := strings.SplitN(string(b), "\n", -1)
	if len(ret) < 2 {
		fmt.Printf("%s:%d", string(b), len(ret))
		return "", nil
	}
	result := ret[len(ret)-2]
	return result, nil

}

func main() {
	Prepare()
	byteContent, err := ioutil.ReadFile(CaseFile)
	if err != nil {
		log.Fatalf("parse case file fail:%v", err)
	}

	var cases Cases
	err = json.Unmarshal(byteContent, &cases)
	if err != nil {
		log.Fatalf("parse cases fail:%v", err)
	}

	//用于记录test case的结果
	recordLog := logrus.New()
	RecordFile := "./record"
	fp, err := os.OpenFile(RecordFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer func() {
		if fp != nil {
			fp.Close()
		}
	}()
	recordLog.Out = fp
	recordLog.Formatter = &MyFormatter{}

	//logDir:存放case中日志的路径
	err = os.Mkdir(logDir, 0777)
	if err != nil {
		if !os.IsExist(err) {
			panic(err)
		}
	}

	for _, testcase := range cases.Case {
		go recorder(testcase)
		command := fmt.Sprintf("%s -lport=\"%s\" -rip=\"%s\" -rport=\"%s\" -user=\"%s\" -passwd=\"%s\" -debug=true", LogicProgram, ServerPort, RegistryIp, RegistryPort, DockerLoginUser, DockerLoginPassword)
		log.Debug(command)
		if testcase.NumPullLimit != 0 {
			command = fmt.Sprintf("%s -plimit=%d", command, testcase.NumPullLimit)
		}
		if testcase.NumPushLimit != 0 {
			command = fmt.Sprintf("%s -limit=%d", command, testcase.NumPushLimit)
		}

		for i := 0; i < testcase.NumTest; i++ {
			logName := fmt.Sprintf("%s/%v_%v_%v_%v.log", logDir, testcase.NumTest, testcase.NumPullLimit, testcase.NumPushLimit, i)
			command = fmt.Sprintf("%s -log=%s", command, logName)
			cmd := exec.Command("/bin/bash", "-c", command)
			//启动Logic server
			go logicRun(testcase, cmd)
			//远程运行test程序
			for _, j := range TestHost {
				go func(host string) {
					client, err := scp.NewScpClient("root", "Cloudsoar123", host)

					if err != nil {
						panic(host + " scp fail " + err.Error())
					}
					defer func() {
						if client != nil {
							client.Close()
						}
					}()
					command := fmt.Sprintf("%s -lport=\"%s\" -rip=\"%s\" -rport=\"%s\" -sip=\"%s\" -sport=\"%s\" &", scp.TestProgram, TestListenPort, RegistryIp, RegistryPort, ServerIp, ServerPort)
					fmt.Println(command)
					_, err = client.Run(command)

				}(j)
			}
			time.Sleep(3 * time.Second)
			//Logic程序结束,一次测试完成
			_ = <-DoneChan
		}
		//记录所有case
		for k, v := range Record {
			recordLog.Infof("[%v]:Success %d, Fail:%d", k, v.Nsuccess, v.Nfail)
			for m, t := range v.Reason {
				recordLog.Infof("Fail Reason: ")
				recordLog.Infof("%%%%%%%v ===> %d() ", m, t)
			}

			for _, i := range v.pullPushCount {
				recordLog.Infof("=====>%v", i)
			}
		}
	}
}

//运行logic server
func logicRun(t CaseStruct, cmd *exec.Cmd) {
	//不需要,cmd运行命令的panic不会上传到当前程序
	/*
		defer func(t CaseStruct) {
			_ = recover()
			failCase := make(map[CaseStruct]string)
			failCase[t] = "panic"
			ResultChan <- failCase
			DoneChan <- 1

		}(t)
	*/

	err := cmd.Run()
	Case := make(map[CaseStruct]string)
	if err != nil {
		if msg, ok := err.(*exec.ExitError); ok {
			Case[t] = msg.Error()
		} else {
			Case[t] = err.Error()
		}
	} else {
		//logic
		Case[t] = "ok"
	}

	ResultChan <- Case
	DoneChan <- 1

}

//记录每个case运行时logic的结果
//以及每次测试pull/push的成功统计
func recorder(t CaseStruct) {
	for {
		select {
		case returnResult := <-ResultChan:
			//解析每次logic运行后产生的pull/push.result文件
			//提取其中pull和push的统计,并进行统计
			pullResult, err := getResult("./pull.result")
			if err != nil {
				log.Error("can't get pullResult")
			}
			pushResult, err := getResult("./push.result")
			if err != nil {
				log.Error("can't get pullResult")
			}

			switch res := returnResult.(type) {
			case map[CaseStruct]string:
				for k, v := range res {
					//老case
					if ret, exists := Record[k]; exists {
						if v == "ok" {
							ret.Nsuccess += 1
							ret.pullPushCount = append(ret.pullPushCount, pullResult+"\n"+pushResult)
							Record[k] = ret
						} else {
							ret.Nfail += 1
							ret.Reason[v] += 1
							ret.pullPushCount = append(ret.pullPushCount, pullResult+"\n"+pushResult)
							Record[k] = ret
						}
						//新case
					} else {
						ret = Result{}
						ret.Reason = make(map[string]int)
						ret.pullPushCount = make([]string, 0)
						if v == "ok" {
							ret.Nsuccess += 1
							ret.pullPushCount = append(ret.pullPushCount, pullResult+"\n"+pushResult)
							Record[k] = ret
						} else {
							ret.Nfail += 1
							ret.Reason[v] += 1
							ret.pullPushCount = append(ret.pullPushCount, pullResult+"\n"+pushResult)
							Record[k] = ret
						}
					}
				}
			}
		}
	}
}
