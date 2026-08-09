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
		fmt.Fprintf(os.Stderr, "warning: .env not loaded from %s: %v\n", envPath, err)
	}
}
