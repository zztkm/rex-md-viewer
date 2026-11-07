package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed skills/rex-md-viewer/SKILL.md
var skillContent []byte

func runSkill(args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "show" {
		_, err := out.Write(skillContent)
		return err
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(out, help)
		return nil
	}
	if len(args) == 0 || args[0] != "install" {
		return usagef("skill show または skill install --dir DIR を指定してください")
	}
	f := flag.NewFlagSet("skill install", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dir := f.String("dir", "", "Skill の親ディレクトリ")
	force := f.Bool("force", false, "上書き")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(out, help)
			return nil
		}
		return usagef("%s", err)
	}
	if *dir == "" || f.NArg() != 0 {
		return usagef("skill install --dir DIR [--force] を指定してください")
	}
	path := filepath.Join(*dir, "rex-md-viewer", "SKILL.md")
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, skillContent) {
			fmt.Fprintln(out, path)
			return nil
		}
		if !*force {
			return fmt.Errorf("%s は内容が異なります。上書きする場合は --force を指定してください", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := atomicWrite(path, skillContent, 0644); err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rex-md-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
