package utils

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/hashicorp/go-getter"
	"github.com/multisig-labs/gogotools/pkg/constants"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

func Fetch(url string, body string) (string, error) {
	client := resty.New()
	// client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	client.SetTimeout(30 * time.Second)

	var resp *resty.Response
	var err error

	if body == "" {
		resp, err = client.R().
			EnableTrace().
			SetHeader("Content-Type", "application/json").
			SetHeader("Accept", "application/json").
			Get(url)
	} else {
		resp, err = client.R().
			EnableTrace().
			SetHeader("Content-Type", "application/json").
			SetHeader("Accept", "application/json").
			SetBody(body).
			Post(url)
	}

	return resp.String(), err
}

func FetchRPC(url string, method string, params string) (string, error) {
	client := resty.New()
	// client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	client.SetTimeout(30 * time.Second)

	var resp *resty.Response
	var err error

	if params == "" {
		params = "{}"
	}

	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id"     : %d,
		"method" : "%s",
		"params" : %s
	}`, time.Now().Unix(), method, params)

	resp, err = client.R().
		EnableTrace().
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json").
		SetBody(body).
		Post(url)

	if resp.IsError() {
		return "", fmt.Errorf("fetch error %d: %s %s", resp.StatusCode(), url, body)
	}
	return resp.String(), err
}

func FetchRPCGJSON(url string, method string, params string) (*gjson.Result, error) {
	s, err := FetchRPC(url, method, params)
	if err != nil {
		return nil, err
	}
	out := gjson.Parse(s)
	return &out, nil
}

func LinkFile(src, dest string) error {
	full, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	return os.Symlink(full, dest)
}

func CopyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Chmod(constants.DefaultPerms755); err != nil {
		return err
	}
	return nil
}

func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return !info.IsDir()
}

func DirExists(dir string) bool {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return info.IsDir()
}

// Create and write a new file
func WriteFileBytes(name string, data []byte) error {
	f, err := os.Create(filepath.Clean(name))
	if err != nil {
		return err
	}
	defer f.Close()

	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}

	return f.Sync()
}

func WatchFile(filePath string) error {
	initialStat, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	for {
		stat, err := os.Stat(filePath)
		if err != nil {
			return err
		}
		if stat.Size() != initialStat.Size() || stat.ModTime() != initialStat.ModTime() {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

func LoadJSON(path string) (*gjson.Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !gjson.Valid(string(b)) {
		return nil, fmt.Errorf("invalid JSON reading %s", path)
	}
	out := gjson.Parse(string(b))
	return &out, nil
}

// From https://goethereumbook.org/util-go/
// Convert from gwei to ether
func ToDecimal(ivalue interface{}, decimals int) decimal.Decimal {
	value := new(big.Int)
	switch v := ivalue.(type) {
	case string:
		value.SetString(v, 0)
	case *big.Int:
		value = v
	}

	mul := decimal.NewFromFloat(float64(10)).Pow(decimal.NewFromFloat(float64(decimals)))
	num, _ := decimal.NewFromString(value.String())
	result := num.Div(mul)

	return result
}

// Given a args array, look for "0.3ether" and convert to wei
func ResolveAmounts(args []string) []string {
	re := regexp.MustCompile("([0-9.]+)ether$")
	wad := big.NewFloat(1e18)

	out := []string{}
	for _, arg := range args {
		matches := re.FindStringSubmatch(arg)
		if len(matches) == 2 {
			amt_f := new(big.Float)
			amt_f.SetString(matches[1])
			amt_fwad := amt_f.Mul(amt_f, wad)
			amt_iwad, _ := amt_fwad.Int(nil)
			out = append(out, amt_iwad.String())
		} else {
			out = append(out, arg)
		}
	}
	return out
}

func ResolveContractAddrs(contracts *gjson.Result, args []string) []string {
	out := []string{}
	for _, arg := range args {
		addr := contracts.Get(arg).String()
		if addr != "" {
			out = append(out, addr)
		} else {
			out = append(out, arg)
		}
	}
	return out
}

func ResolveAccountAddrs(accounts *gjson.Result, args []string) []string {
	out := []string{}
	for _, arg := range args {
		addr := accounts.Get(arg).Get("addr").String()
		if addr != "" {
			out = append(out, addr)
		} else {
			out = append(out, arg)
		}
	}
	return out
}

func DownloadAvalanchego(destDir string, version string) (url string, destFile string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	fn := fmt.Sprintf("avalanchego-%s", version)
	destFile = filepath.Join(destDir, fn)
	if FileExists(destFile) {
		return url, destFile, fmt.Errorf("file exists: %s", destFile)
	}

	tdir, err := os.MkdirTemp("", "ggt")
	if err != nil {
		return url, destFile, err
	}
	defer func() {
		os.RemoveAll(tdir)
	}()

	var exeFile string
	switch goos {
	case "darwin":
		url = fmt.Sprintf(
			"https://github.com/ava-labs/avalanchego/releases/download/%s/avalanchego-macos-%s.zip",
			version,
			version,
		)
		// It unzips into a 'build' folder
		exeFile = filepath.Join(tdir, "build", "avalanchego")
	case "linux":
		url = fmt.Sprintf(
			"https://github.com/ava-labs/avalanchego/releases/download/%s/avalanchego-linux-%s-%s.tar.gz",
			version,
			goarch,
			version,
		)
		exeFile = filepath.Join(tdir, fmt.Sprintf("avalanchego-%s", version), "avalanchego")
	default:
		return url, destFile, fmt.Errorf("downloading not supported on OS: %s", goos)
	}

	err = getter.GetAny(tdir, url)
	if err != nil {
		return url, destFile, err
	}

	err = CopyFile(exeFile, destFile)

	return url, destFile, err
}

func DownloadSubnetevm(destDir string, version string) (url string, destFile string, err error) {
	goarch := runtime.GOARCH
	goos := runtime.GOOS
	switch goos {
	case "darwin":
		url = fmt.Sprintf(
			"https://github.com/ava-labs/subnet-evm/releases/download/%s/subnet-evm_%s_darwin_%s.tar.gz",
			version,
			version[1:],
			goarch,
		)
	case "linux":
		url = fmt.Sprintf(
			"https://github.com/ava-labs/subnet-evm/releases/download/%s/subnet-evm_%s_linux_%s.tar.gz",
			version,
			version[1:],
			goarch,
		)
	default:
		return "", "", fmt.Errorf("downloading not supported on OS: %s", goos)
	}

	fn := fmt.Sprintf("subnet-evm-%s", version)
	destFile = filepath.Join(destDir, fn)
	if FileExists(destFile) {
		return "", "", fmt.Errorf("file exists: %s", destFile)
	}

	tdir, err := os.MkdirTemp("", "ggt")
	if err != nil {
		return "", "", err
	}
	defer func() {
		os.RemoveAll(tdir)
	}()

	err = getter.GetAny(tdir, url)
	if err != nil {
		return "", "", err
	}

	err = CopyFile(filepath.Join(tdir, "subnet-evm"), destFile)

	return url, destFile, err
}

// func AvaKeyToEthKey(key *crypto.PrivateKeySECP256K1R) common.Address {
// 	pubk := key.ToECDSA().PublicKey
// 	addr := ethcrypto.PubkeyToAddress(pubk)
// 	return addr
// }
