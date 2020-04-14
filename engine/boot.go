package engine

func Boot(cp string) {
	conf, err := Setup(cp)
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
