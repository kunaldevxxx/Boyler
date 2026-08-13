package imageservice

type PullCommand struct {
	ImageIdentify string
}

type RemoveCommand struct {
	ImageIdentify string
	Force         bool
}

type PruneCommand struct {
	All    bool
	DryRun bool
}
