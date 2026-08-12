package cmd

import (
	"fmt"
	"github.com/joho/godotenv"
	"os"
	"path/filepath"
)

func loadEnv() {
	exePath, _ := os.Executable()
	resPath, _ := filepath.EvalSymlinks(exePath)
	binDir := filepath.Dir(resPath)
	envPath := filepath.Join(filepath.Dir(binDir), ".env")
	if err := godotenv.Load(envPath); err != nil {
		// A .env file is optional: production installs commonly provide their
		// configuration through the process environment.
		if !os.IsNotExist(err) {
			printWarning(os.Stderr, fmt.Sprintf("could not load %s: %v", envPath, err))
		}
	}
}
