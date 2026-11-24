package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/tui"
)

const DefaultSocket = "/run/nix-connect/control.sock"

func Run(args []string) error {
	global := flag.NewFlagSet("nixconnect", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	global.Usage = func() {
		usage(os.Stderr)
	}
	socket := global.String("socket", DefaultSocket, "local control socket")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := global.Args()
	client := httpapi.NewLocal(*socket)
	if len(rest) == 0 {
		return tui.Run(client)
	}

	command := rest[0]
	arguments := rest[1:]
	ctx := context.Background()

	switch command {
	case "status":
		return printJSON(client.Status(ctx))
	case "devices":
		return printJSON(client.Devices(ctx))
	case "info":
		return info(ctx, client, arguments)
	case "ssh":
		return ssh(ctx, client, arguments)
	case "jobs":
		return printJSON(client.Jobs(ctx))
	case "job":
		return job(ctx, client, arguments)
	case "confirm":
		return confirm(ctx, client, arguments)
	case "cancel":
		return cancel(ctx, client, arguments)
	case "register":
		return register(ctx, client, arguments)
	case "unregister":
		return unregister(ctx, client, arguments)
	case "deploy":
		return deploy(ctx, client, arguments, false)
	case "update":
		return deploy(ctx, client, arguments, true)
	case "publish":
		return publish(ctx, client, arguments)
	case "build-iso":
		return buildISO(ctx, client, arguments)
	case "reset":
		return client.ResetRegistration(ctx)
	case "trust":
		return trustCommand(ctx, client, arguments)
	case "pair":
		return pair(ctx, client, arguments)
	case "key":
		return key(ctx, client, arguments)
	case "help", "--help", "-h":
		usage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("nixconnect: unknown command %q", command)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `nixconnect [--socket PATH] <command>

Commands:
  status                    show daemon status
  devices                   list discovered devices
  info <id>                 show a device
  ssh <id>                  open an SSH session through the daemon agent
  jobs                      list jobs
  job <id>                  show a job
  confirm <id>              confirm a magic rollback job
  cancel <id>               cancel a job
  register <id>             register a flake with a device
  unregister <id>           remove a device registration
  deploy <id>               deploy a different flake to an installed device
  update <id>               update from the registered flake
  publish                   publish a no-commit device to a new remote
  reset                     reset the registration to no-commit
  trust list                list paired devices
  trust revoke <id>         revoke a paired device
  build-iso [--output PATH] build a deploytarget ISO out-link
  pair start <id> [key]     start pairing
  pair pending              list pending pairing requests
  pair confirm <id> <code>  confirm pairing
  key share <id>            obtain and install the administration key
  key rotate                rotate the administration key

With no command, nixconnect opens the TUI.`)
}

func info(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("nixconnect: info requires a device id")
	}
	devices, err := client.Devices(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.ID == args[0] || device.ShortID == args[0] {
			return printJSON(device)
		}
	}
	return fmt.Errorf("nixconnect: unknown device %q", args[0])
}

func ssh(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("nixconnect: ssh requires a device id")
	}
	info, err := client.SSH(ctx, args[0])
	if err != nil {
		return err
	}
	path, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	environment := os.Environ()
	if info.AgentSocket != "" {
		environment = append(environment, "SSH_AUTH_SOCK="+info.AgentSocket)
	}
	sshArgs := []string{"ssh", "-p", fmt.Sprint(info.Port), info.User + "@" + info.Host}
	return syscall.Exec(path, sshArgs, environment)
}

func job(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("nixconnect: job requires a job id")
	}
	job, err := client.Job(ctx, args[0])
	if err != nil {
		return err
	}
	return printJSON(job)
}

func confirm(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("nixconnect: confirm requires a job id")
	}
	return client.Confirm(ctx, args[0])
}

func cancel(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("nixconnect: cancel requires a job id")
	}
	return client.Cancel(ctx, args[0])
}

func register(ctx context.Context, client *httpapi.Client, args []string) error {
	set := flag.NewFlagSet("register", flag.ContinueOnError)
	repository := set.String("flake", "", "flake git URL")
	branch := set.String("branch", "main", "flake branch")
	host := set.String("host", "", "nixosConfigurations host")
	account := set.String("account", "", "account used for consent")
	sshUser := set.String("ssh-user", "", "SSH user")
	sshPort := set.Int("ssh-port", 0, "SSH port")
	password := set.String("password", "", "consent password")
	passwordStdin := set.Bool("password-stdin", false, "read the consent password from stdin")
	id, err := parseDeviceCommand(set, args, "register")
	if err != nil {
		return err
	}
	secret, err := passwordValue(*password, *passwordStdin)
	if err != nil {
		return err
	}
	return client.Register(ctx, id, httpapi.RegisterRequest{
		Repository: *repository,
		Branch:     *branch,
		Host:       *host,
		Account:    *account,
		SSHUser:    *sshUser,
		SSHPort:    *sshPort,
		Password:   secret,
	})
}

func unregister(ctx context.Context, client *httpapi.Client, args []string) error {
	set := flag.NewFlagSet("unregister", flag.ContinueOnError)
	password := set.String("password", "", "consent password")
	passwordStdin := set.Bool("password-stdin", false, "read the consent password from stdin")
	id, err := parseDeviceCommand(set, args, "unregister")
	if err != nil {
		return err
	}
	secret, err := passwordValue(*password, *passwordStdin)
	if err != nil {
		return err
	}
	return client.Unregister(ctx, id, secret)
}

func deploy(ctx context.Context, client *httpapi.Client, args []string, update bool) error {
	set := flag.NewFlagSet("deploy", flag.ContinueOnError)
	repository := set.String("flake", "", "flake git URL")
	branch := set.String("branch", "main", "flake branch")
	host := set.String("host", "", "nixosConfigurations host")
	disk := set.String("disk", "", "target disk device for a live install")
	account := set.String("account", "", "target account for a live install")
	adminKey := set.String("admin-key", "", "administration SSH public key")
	firmware := set.String("firmware", "efi", "target firmware: efi or bios")
	password := set.String("password", "", "consent password")
	passwordStdin := set.Bool("password-stdin", false, "read the consent password from stdin")
	mode := set.String("mode", "", "magic rollback mode: automatic or manual")
	magicOn := set.Bool("magic-rollback", false, "force magic rollback on")
	noMagic := set.Bool("no-magic-rollback", false, "force magic rollback off")
	timeout := set.Int("timeout", 0, "confirmation timeout in seconds")
	id, err := parseDeviceCommand(set, args, "deploy")
	if err != nil {
		return err
	}
	secret, err := passwordValue(*password, *passwordStdin)
	if err != nil {
		return err
	}
	var magic *bool
	if *magicOn {
		value := true
		magic = &value
	}
	if *noMagic {
		value := false
		magic = &value
	}
	if update {
		job, err := client.Update(ctx, id, httpapi.UpdateRequest{
			Password:      secret,
			Mode:          *mode,
			MagicRollback: magic,
			Timeout:       *timeout,
		})
		if err != nil {
			return err
		}
		fmt.Println(job)
		return nil
	}
	job, err := client.Deploy(ctx, id, httpapi.DeployRequest{
		Repository:    *repository,
		Branch:        *branch,
		Host:          *host,
		Disk:          *disk,
		Account:       *account,
		AdminKey:      *adminKey,
		Firmware:      *firmware,
		Password:      secret,
		Mode:          *mode,
		MagicRollback: magic,
		Timeout:       *timeout,
	})
	if err != nil {
		return err
	}
	fmt.Println(job)
	return nil
}

func publish(ctx context.Context, client *httpapi.Client, args []string) error {
	set := flag.NewFlagSet("publish", flag.ContinueOnError)
	remote := set.String("remote", "", "new empty git remote")
	branch := set.String("branch", "", "branch to push")
	password := set.String("password", "", "consent password")
	passwordStdin := set.Bool("password-stdin", false, "read the consent password from stdin")
	if err := set.Parse(args); err != nil {
		return err
	}
	secret, err := passwordValue(*password, *passwordStdin)
	if err != nil {
		return err
	}
	job, err := client.Publish(ctx, httpapi.PublishRequest{
		RemoteURL: *remote,
		Branch:    *branch,
		Password:  secret,
	})
	if err != nil {
		return err
	}
	fmt.Println(job)
	return nil
}

func trustCommand(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("nixconnect: trust list|revoke <id>")
	}
	switch args[0] {
	case "list":
		records, err := client.TrustRecords(ctx)
		return printJSON(records, err)
	case "revoke":
		if len(args) != 2 {
			return errors.New("nixconnect: trust revoke <id>")
		}
		return client.RevokeTrust(ctx, args[1])
	default:
		return errors.New("nixconnect: trust list|revoke <id>")
	}
}

func buildISO(ctx context.Context, client *httpapi.Client, args []string) error {
	set := flag.NewFlagSet("build-iso", flag.ContinueOnError)
	output := "deploy-target-iso"
	set.StringVar(&output, "o", output, "output link path")
	set.StringVar(&output, "output", output, "output link path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("nixconnect: build-iso [--output PATH]")
	}
	destination, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	job, err := client.BuildISO(ctx, httpapi.ISORequest{Output: destination})
	if err != nil {
		return err
	}
	fmt.Println(job)
	fmt.Println(destination)
	return nil
}

func pair(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) == 1 && args[0] == "pending" {
		pending, err := client.PairPending(ctx)
		if err != nil {
			return err
		}
		return printJSON(pending)
	}
	if len(args) < 2 {
		return errors.New("nixconnect: pair start <id> [key], pair pending or pair confirm <id> <code>")
	}
	switch args[0] {
	case "start":
		if len(args) != 2 && len(args) != 3 {
			return errors.New("nixconnect: pair start <id> [key]")
		}
		key := ""
		if len(args) == 3 {
			key = args[2]
		}
		response, err := client.PairStart(ctx, httpapi.PairRequest{PeerID: args[1], PeerKey: key})
		if err != nil {
			return err
		}
		return printJSON(response)
	case "confirm":
		if len(args) != 3 {
			return errors.New("nixconnect: pair confirm <id> <code>")
		}
		return client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: args[1], Code: args[2]})
	default:
		return errors.New("nixconnect: pair start|pending|confirm")
	}
}

func key(ctx context.Context, client *httpapi.Client, args []string) error {
	if len(args) == 1 && args[0] == "rotate" {
		publicKey, err := client.RotateAdminKey(ctx)
		if err != nil {
			return err
		}
		fmt.Println(publicKey)
		return nil
	}
	if len(args) != 2 || args[0] != "share" {
		return errors.New("nixconnect: key share <id> or key rotate")
	}
	status, err := client.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Println("key sharing code:", pairing.Code(status.ID, args[1]))
	publicKey, err := client.ImportSharedKey(ctx, args[1])
	if err != nil {
		return err
	}
	fmt.Println(publicKey)
	return nil
}

func parseDeviceCommand(set *flag.FlagSet, args []string, command string) (string, error) {
	var flags []string
	var positional []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			positional = append(positional, argument)
			continue
		}
		flags = append(flags, argument)
		name := strings.TrimLeft(argument, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if flagTakesValue(set, name) {
			if index+1 >= len(args) {
				return "", fmt.Errorf("nixconnect: %s: missing value for %s", command, argument)
			}
			index++
			flags = append(flags, args[index])
		}
	}
	if err := set.Parse(flags); err != nil {
		return "", err
	}
	if len(positional) != 1 {
		return "", fmt.Errorf("nixconnect: %s requires a device id", command)
	}
	return positional[0], nil
}

func flagTakesValue(set *flag.FlagSet, name string) bool {
	flag := set.Lookup(name)
	if flag == nil {
		return false
	}
	if boolean, ok := flag.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
		return false
	}
	return true
}

func passwordValue(value string, stdin bool) (string, error) {
	if !stdin {
		return value, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func printJSON(value any, errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
