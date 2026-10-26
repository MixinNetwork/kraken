package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/MixinNetwork/kraken/engine"
	"github.com/MixinNetwork/mixin/logger"
)

const Version = "0.4.0"

func main() {
	cp := flag.String("c", "~/.kraken/engine.toml", "configuration file path")
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 {
		fmt.Println(Version)
		return
	}

	if strings.HasPrefix(*cp, "~/") {
		usr, _ := user.Current()
		*cp = filepath.Join(usr.HomeDir, (*cp)[2:])
	}

	logger.SetLevel(logger.VERBOSE)

	go func() {
		err := http.ListenAndServe(":9000", http.DefaultServeMux)
		if err != nil {
			panic(err)
		}
	}()

	engine.Boot(*cp, Version)
}
