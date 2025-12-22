package master

func Run(args []string) error {
	if len(args) > 0 {
		return handleCLI(args)
	}
	return runServer()
}
