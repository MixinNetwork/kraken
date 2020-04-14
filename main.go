package main

import (
	"flag"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/MixinNetwork/mixin/logger"
)

func main() {
	cp := flag.String("config", "~/.kraken/config.toml", "the configuration file path")
	flag.Parse()

	if strings.HasPrefix(*cp, "~/") {
		usr, _ := user.Current()
		*cp = filepath.Join(usr.HomeDir, (*cp)[2:])
	}

	logger.SetLevel(logger.INFO)
	conf, err := Setup(*cp)
	if err != nil {
		panic(err)
	}

	engine, err := BuildEngine(conf)
	if err != nil {
		panic(err)
	}

	go engine.Loop()
	ServeRPC(engine, conf)
}
