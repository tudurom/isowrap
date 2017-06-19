package isowrap

import (
	"bufio"
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// BoxRunner is a Runner based on isolate (See README)
type BoxRunner struct {
	B *Box
}

// See the isolate(1) for the format of the meta file returned by isolate.
func parseMetaFile(fp string) (map[string]string, error) {
	ret := make(map[string]string)
	file, err := os.Open(fp)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		runes := []rune(scanner.Text())
		i := 0
		ln := len(runes)
		key := ""
		for i < ln && runes[i] != ':' {
			key += string(runes[i])
			i++
		}
		i++ // skip over ':'
		value := ""
		for i < ln {
			value += string(runes[i])
			i++
		}

		ret[key] = value
	}

	return ret, nil
}

// Init creates a new isolate box with control group support
func (br *BoxRunner) Init() error {
	params := []string{}
	params = append(
		params,
		"--cg",
		"--box-id="+strconv.Itoa(int(br.B.ID)),
		"--init",
	)
	stdout, _, _, err := Exec("isolate", params...)
	if err != nil {
		return err
	}

	br.B.Path = strings.TrimSpace(string(stdout)) + "/box"
	return nil
}

// Run runs the specified command inside the isolated box
func (br *BoxRunner) Run(command string) (result RunResult, err error) {
	itoa := func(i uint) string {
		return strconv.Itoa(int(i))
	}

	metaFile, err := ioutil.TempFile("", "isowrap")
	if err != nil {
		return
	}
	metaFileName := metaFile.Name()
	err = metaFile.Close()
	if err != nil {
		return
	}

	params := []string{}
	params = append(params, "--silent", "-M"+metaFile.Name())

	ap := func(p string, i uint) {
		if i > 0 {
			params = append(params, p+"="+itoa(i))
		}
	}

	apf := func(p string, i float64) {
		if i > 0 {
			params = append(params, p+"="+strconv.FormatFloat(i, 'f', -1, 64))
		}
	}

	params = append(params, "--box-id="+itoa(br.B.ID))
	apf("--time", br.B.Config.CPUTime)
	apf("--wall-time", br.B.Config.WallTime)
	ap("--stack", br.B.Config.StackLimit)

	if br.B.Config.MaxProc == 0 {
		params = append(params, "-p")
	} else {
		params = append(params, "--processes="+itoa(br.B.Config.MaxProc))
	}

	if br.B.Config.ShareNetwork {
		params = append(params, "--share-net")
	}

	for _, e := range br.B.Config.Env {
		if e.Value == "" {
			params = append(params, "--env="+e.Var)
		} else {
			params = append(params, "--env="+e.Var+"="+e.Value)
		}
	}

	params = append(params, "--cg", "--run", "--", command)
	stdout, stderr, _, err := Exec("isolate", params...)
	result.Stdout = stdout
	result.Stderr = stderr
	meta, err := parseMetaFile(metaFileName)
	if err != nil {
		return
	}

	// YOLO

	result.CPUTime, _ = strconv.ParseFloat(meta["time"], 64)
	result.WallTime, _ = strconv.ParseFloat(meta["time-wall"], 64)
	memused, _ := strconv.ParseUint(meta["cg-mem"], 10, 64)
	result.MemUsed = uint(memused)
	if _, ok := meta["status"]; ok {
		result.ErrorType = BoxError(NoError)
	}
	switch meta["status"] {
	case "RE":
		result.ErrorType = BoxError(RunTimeError)
	case "SG":
		result.ErrorType = BoxError(KilledBySignal)
	case "TO":
		result.ErrorType = BoxError(Timeout)
	case "XX":
		result.ErrorType = BoxError(InternalError)
	case "":
	default:
		return RunResult{}, errors.New("Unknown run status " + meta["status"])
	}
	result.ExitCode, _ = strconv.Atoi(meta["exitcode"])

	return
}

// Cleanup cleans up the isolate box
func (br *BoxRunner) Cleanup() error {
	_, err := exec.Command("isolate", "--cg", "--box-id="+strconv.Itoa(int(br.B.ID)), "--cleanup").Output()
	if err != nil {
		return err
	}

	return nil
}