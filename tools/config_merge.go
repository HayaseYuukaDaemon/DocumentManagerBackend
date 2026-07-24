package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"document-archive/internal/config"

	"gopkg.in/yaml.v3"
)

func main() {
	inputPath := flag.String("in", "config.yml", "partial config path, or - for stdin")
	outputPath := flag.String("out", "-", "merged config path, or - for stdout")
	flag.Parse()

	input, err := readInput(*inputPath)
	if err != nil {
		exitError(err)
	}
	merged, err := mergeConfig(input)
	if err != nil {
		exitError(err)
	}
	if err := writeOutput(*outputPath, merged); err != nil {
		exitError(err)
	}
}

func mergeConfig(input []byte) ([]byte, error) {
	cfg := config.Default()
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && err != io.EOF {
		return nil, fmt.Errorf("parse partial config: %w", err)
	}

	merged, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	return merged, nil
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return input, nil
	}
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return input, nil
}

func writeOutput(path string, content []byte) error {
	if path == "-" {
		if _, err := os.Stdout.Write(content); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func exitError(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
