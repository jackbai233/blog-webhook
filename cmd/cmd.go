package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const version = "0.1.0"

var (
	app       = kingpin.New("blog-webhook", "BlogWebhook").Version(version)
	shellFile = app.Flag("shell-file", "shell file (.sh) to execute").String()
)

func Run() {
	// recover the panic.
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("start blog-webhook server failed:：%v\nstack：%v", err, string(debug.Stack()))
		}
	}()

	kingpin.MustParse(app.Parse(os.Args[1:]))
	// Check shellFile.
	if shellFile == nil {
		fmt.Println("you do not add the shell file on the command line.")
	}
	// initial server port and service endpoint.
	host := "0.0.0.0"
	port := 10002
	svcSuffix := "/run"
	mux := http.NewServeMux()
	// add cmd execute service
	mux.HandleFunc(svcSuffix, ShellExecHandler)
	// stop server gracefully
	// refer: https://github.com/gin-gonic/examples/blob/master/graceful-shutdown/graceful-shutdown/server.go
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Println("listen error: ", err)
		}
	}()

	fmt.Printf("webhook server is running at %s:%d%s", host, port, svcSuffix)

	// Wait for interrupt signal to gracefully shut down the server with
	// a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be caught, so don't need to add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Println("Shutting down server...")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:gomnd
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Println("Server forced to shutdown: ", err)
	}

	fmt.Println("Server exiting")
}

// ShellExecHandler add shell execute handler
func ShellExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		resp := Resp{
			Code: http.StatusMethodNotAllowed,
			Msg:  "illegal request method",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	fmt.Println("start executing shell file：", *shellFile)
	execute := false

	timeoutChan := make(chan struct{})
	retChan := make(chan Resp)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(20)*time.Second)
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				if !execute {
					// broadcast
					close(timeoutChan)
				}
				return
			}
		}
	}()

	// execute the command
	go func() {
		code := Ok
		msg := CustomError[code]
		ret, err := executeShell(*shellFile)
		if err != nil {
			code = NotOk
			msg = fmt.Sprintf("%s:%s", CustomError[code], err)
		}
		resp := Resp{
			Code: code,
			Data: ret,
			Msg:  msg,
		}
		retChan <- resp
		execute = true
	}()

	select {
	case <-timeoutChan:
		resp := Resp{
			Code: NotOk,
			Msg:  "the shell file executes timeout",
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	case ret := <-retChan:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ret)
	}
}

// executeShell  execute the cmd
func executeShell(filename string) (string, error) {
	var filepath string
	// config file suffix
	filenameWithSuffix := path.Base(filename)
	fileSuffix := path.Ext(filenameWithSuffix)
	if fileSuffix != ".sh" {
		return "", errors.New("the shell file is not shell script")
	}

	if filename == "" {
		return "", errors.New("the shell file should not empty")
	}
	if strings.HasPrefix(filename, "/") {
		filepath = filename
	} else {
		pwd, _ := os.Getwd()
		filepath = pwd + "/" + filename
	}
	// config filepath.
	bs, err := os.ReadFile(filename)
	if len(bs) == 0 || err != nil {
		return "", fmt.Errorf("shell file: %s not exist", filename)
	}
	// execute
	cmd := exec.Command("/bin/sh", "-c", filepath)
	data, err := cmd.CombinedOutput()
	return string(data), err
}

type Resp struct {
	Code int         `json:"code"` // 错误代码
	Data interface{} `json:"data"` // 数据内容
	Msg  string      `json:"msg"`  // 消息提示
}

const (
	Ok       = 201
	NotOk    = 405
	OkMsg    = "execute successfully"
	NotOkMsg = "execute failed"
)

var CustomError = map[int]string{
	Ok:    OkMsg,
	NotOk: NotOkMsg,
}
