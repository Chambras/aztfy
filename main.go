package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"flag"

	"github.com/Azure/aztfy/internal/config"
	"github.com/Azure/aztfy/internal/meta"
	"github.com/Azure/aztfy/internal/ui"
)

var (
	flagVersion     *bool
	flagOutputDir   *string
	flagMappingFile *string
	flagContinue    *bool
	flagQuietMode   *bool
	flagPattern     *string
)

func init() {
	flagVersion = flag.Bool("v", false, "Print version")
	flagOutputDir = flag.String("o", "", "Specify output dir. Default is a dir under the user cache dir, which is named after the resource group name")
	flagMappingFile = flag.String("m", "", "Specify the resource mapping file")
	flagContinue = flag.Bool("k", false, "Whether continue on import error (quiet mode only)")
	flagQuietMode = flag.Bool("q", false, "Quiet mode")
	flagPattern = flag.String("p", "res-", `The pattern of the resource name. The resource name is generated by taking the pattern and adding an auto-incremental integer to the end. If pattern includes a "*", the auto-incremental integer replaces the last "*".`)
}

const usage = `aztfy [option] <resource group name>
`

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "%s\n", usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *flagVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Flag sanity check
	if len(flag.Args()) != 1 {
		flag.Usage()
		os.Exit(1)
	}
	if *flagQuietMode && *flagMappingFile == "" {
		fatal(errors.New("`-q` must be used together with `-m`"))
	}
	if *flagContinue && !*flagQuietMode {
		fatal(errors.New("`-k` must be used together with `-q`"))
	}

	rg := flag.Args()[0]

	cfg, err := config.NewConfig(rg, *flagOutputDir, *flagMappingFile, *flagPattern)
	if err != nil {
		fatal(err)
	}

	if *flagQuietMode {
		if err := batchImport(*cfg, *flagContinue); err != nil {
			fatal(err)
		}
		return
	}

	prog, err := ui.NewProgram(*cfg)
	if err != nil {
		fatal(err)
	}

	if err := prog.Start(); err != nil {
		fatal(err)
	}
}

func batchImport(cfg config.Config, continueOnError bool) error {
	// Discard logs from hashicorp/azure-go-helper
	log.SetOutput(io.Discard)
	// Define another dedicated logger for the ui
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if cfg.Logfile != "" {
		f, err := os.OpenFile(cfg.Logfile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		logger = log.New(f, "aztfy", log.LstdFlags)
	}

	logger.Println("New meta")
	c, err := meta.NewMeta(cfg)
	if err != nil {
		return err
	}

	logger.Println("Initialize")
	if err := c.Init(); err != nil {
		return err
	}

	logger.Println("List resources")
	list := c.ListResource()

	logger.Println("Import resources")
	for i := range list {
		if list[i].Skip() {
			logger.Printf("[WARN] No mapping information for resource: %s, skip it\n", list[i].ResourceID)
			continue
		}
		logger.Printf("Importing %s as %s\n", list[i].ResourceID, list[i].TFAddr())
		c.Import(&list[i])
		if err := list[i].ImportError; err != nil {
			msg := fmt.Sprintf("Failed to import %s as %s: %v", list[i].ResourceID, list[i].TFAddr(), err)
			if !continueOnError {
				return fmt.Errorf(msg)
			}
			logger.Println("[ERROR] " + msg)
		}
	}

	logger.Println("Generate Terraform configurations")
	if err := c.GenerateCfg(list); err != nil {
		return fmt.Errorf("generating Terraform configuration: %v", err)
	}

	return nil
}
