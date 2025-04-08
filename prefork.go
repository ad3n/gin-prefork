package prefork

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-reuseport"
)

const (
	preforkKey = "AD3N_PREFORK_CHILD"
	preforkVal = "1"
)

type prefork struct {
	engine *gin.Engine
}

func New(engine *gin.Engine) *prefork {
	return &prefork{engine: engine}
}

func IsChild() bool {
	return os.Getenv(preforkKey) == preforkVal
}

func (p prefork) StartTLS(address string, tlsConfig *tls.Config) error {
	return fork(p.engine, address, tlsConfig)
}

func (p prefork) Start(address string) error {
	return fork(p.engine, address, nil)
}

func fork(engine *gin.Engine, address string, tlsConfig *tls.Config) error {
	var ln net.Listener
	var err error

	if IsChild() {
		runtime.GOMAXPROCS(1)

		ln, err = reuseport.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("prefork: %s", err.Error())
		}

		if tlsConfig != nil {
			ln = tls.NewListener(ln, tlsConfig)
		}

		go watchMaster()

		return engine.RunListener(ln)
	}

	type child struct {
		err error
		pid int
	}

	maxProcs := runtime.GOMAXPROCS(0)
	childs := make(map[int]*exec.Cmd)
	channel := make(chan child, maxProcs)

	defer func() {
		for _, proc := range childs {
			if err = proc.Process.Kill(); err != nil {
				if !errors.Is(err, os.ErrProcessDone) {
				}
			}
		}
	}()

	pids := make([]int, 0, maxProcs)
	for range maxProcs {
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		env := strings.Builder{}
		env.WriteString(preforkKey)
		env.WriteString("=")
		env.WriteString(preforkVal)

		cmd.Env = append(os.Environ(), env.String())

		if err = cmd.Start(); err != nil {
			return fmt.Errorf("failed to start a child prefork process, error: %s", err.Error())
		}

		pid := cmd.Process.Pid
		childs[pid] = cmd
		pids = append(pids, pid)

		go func() {
			channel <- child{pid: pid, err: cmd.Wait()}
		}()
	}

	return (<-channel).err
}

func watchMaster() {
	if runtime.GOOS == "windows" {
		p, err := os.FindProcess(os.Getppid())
		if err == nil {
			_, _ = p.Wait()
		}

		os.Exit(1)
	}

	const watchInterval = 500 * time.Millisecond
	for range time.NewTicker(watchInterval).C {
		if os.Getppid() == 1 {
			os.Exit(1)
		}
	}
}
