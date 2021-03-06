package util

import (
	"archive/tar"
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v2"

	log "github.com/Sirupsen/logrus"

	"github.com/docker/docker/pkg/mount"
	"reflect"
)

var (
	letters      = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	ErrNoNetwork = errors.New("Networking not available to load resource")
	ErrNotFound  = errors.New("Failed to find resource")
)

func GetOSType() string {
	f, err := os.Open("/etc/os-release")
	defer f.Close()
	if err != nil {
		return "busybox"
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 8 && line[:8] == "ID_LIKE=" {
			return line[8:]
		}
	}
	return "busybox"

}

func Remount(directory, options string) error {
	return mount.Mount("", directory, "", fmt.Sprintf("remount,%s", options))
}

func ExtractTar(archive string, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	input := tar.NewReader(f)

	for {
		header, err := input.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		if header == nil {
			break
		}

		fileInfo := header.FileInfo()
		fileName := path.Join(dest, header.Name)
		if fileInfo.IsDir() {
			//log.Debugf("DIR : %s", fileName)
			err = os.MkdirAll(fileName, fileInfo.Mode())
			if err != nil {
				return err
			}
		} else {
			//log.Debugf("FILE: %s", fileName)
			destFile, err := os.OpenFile(fileName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fileInfo.Mode())
			if err != nil {
				return err
			}

			_, err = io.Copy(destFile, input)
			// Not deferring, concerned about holding open too many files
			destFile.Close()

			if err != nil {
				return err
			}
		}
	}

	return nil
}

func Contains(values []string, value string) bool {
	if len(value) == 0 {
		return false
	}

	for _, i := range values {
		if i == value {
			return true
		}
	}

	return false
}

type ReturnsErr func() error

func ShortCircuit(funcs ...ReturnsErr) error {
	for _, f := range funcs {
		err := f()
		if err != nil {
			return err
		}
	}

	return nil
}

type ErrWriter struct {
	w   io.Writer
	Err error
}

func NewErrorWriter(w io.Writer) *ErrWriter {
	return &ErrWriter{
		w: w,
	}
}

func (e *ErrWriter) Write(buf []byte) *ErrWriter {
	if e.Err != nil {
		return e
	}

	_, e.Err = e.w.Write(buf)
	return e
}

func RandSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func Convert(from, to interface{}) error {
	bytes, err := yaml.Marshal(from)
	if err != nil {
		log.WithFields(log.Fields{"from": from, "err": err}).Warn("Error serializing to YML")
		return err
	}

	return yaml.Unmarshal(bytes, to)
}

func MergeBytes(left, right []byte) ([]byte, error) {
	leftMap := make(map[interface{}]interface{})
	rightMap := make(map[interface{}]interface{})

	if err := yaml.Unmarshal(left, &leftMap); err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(right, &rightMap); err != nil {
		return nil, err
	}

	return yaml.Marshal(MapsUnion(leftMap, rightMap, Replace))
}

func Copy(d interface{}) interface{} {
	switch d := d.(type) {
	case map[interface{}]interface{}:
		return MapCopy(d)
	case []interface{}:
		return SliceCopy(d)
	default:
		return d
	}
}

func Replace(l, r interface{}) interface{} {
	return r
}

func Equal(l, r interface{}) interface{} {
	if reflect.DeepEqual(l, r) {
		return l
	}
	return nil
}

func ExistsIn(x interface{}, s []interface{}) bool {
	for _, y := range s {
		if reflect.DeepEqual(x, y) {
			return true
		}
	}
	return false
}

func SlicesUnion(left, right []interface{}, op func(interface{}, interface{}) interface{}) []interface{} {
	result := SliceCopy(left)
	for _, r := range right {
		if !ExistsIn(r, result) {
			result = append(result, r)
		}
	}
	return result
}

func SlicesIntersection(left, right []interface{}, op func(interface{}, interface{}) interface{}) []interface{} {
	result := []interface{}{}
	for _, r := range right {
		if ExistsIn(r, left) {
			result = append(result, r)
		}
	}
	return result
}

func MapsUnion(left, right map[interface{}]interface{}, op func(interface{}, interface{}) interface{}) map[interface{}]interface{} {
	result := MapCopy(left)

	for k, r := range right {
		if l, ok := left[k]; ok {
			switch l := l.(type) {
			case map[interface{}]interface{}:
				switch r := r.(type) {
				case map[interface{}]interface{}:
					result[k] = MapsUnion(l, r, op)
				default:
					result[k] = op(l, r)
				}
			case []interface{}:
				switch r := r.(type) {
				case []interface{}:
					result[k] = SlicesUnion(l, r, op)
				default:
					result[k] = op(l, r)
				}
			default:
				result[k] = op(l, r)
			}
		} else {
			result[k] = Copy(r)
		}
	}

	return result
}

func MapsIntersection(left, right map[interface{}]interface{}, op func(interface{}, interface{}) interface{}) map[interface{}]interface{} {
	result := map[interface{}]interface{}{}

	for k, l := range left {
		if r, ok := right[k]; ok {
			switch l := l.(type) {
			case map[interface{}]interface{}:
				switch r := r.(type) {
				case map[interface{}]interface{}:
					result[k] = MapsIntersection(l, r, op)
				default:
					if v := op(l, r); v != nil {
						result[k] = v
					}
				}
			case []interface{}:
				switch r := r.(type) {
				case []interface{}:
					result[k] = SlicesIntersection(l, r, op)
				default:
					if v := op(l, r); v != nil {
						result[k] = v
					}
				}
			default:
				if v := op(l, r); v != nil {
					result[k] = v
				}
			}
		}
	}

	return result
}

func MapCopy(data map[interface{}]interface{}) map[interface{}]interface{} {
	result := map[interface{}]interface{}{}
	for k, v := range data {
		result[k] = Copy(v)
	}
	return result
}

func SliceCopy(data []interface{}) []interface{} {
	result := make([]interface{}, len(data), len(data))
	for k, v := range data {
		result[k] = Copy(v)
	}
	return result
}

func GetServices(urls []string) ([]string, error) {
	result := []string{}

	for _, url := range urls {
		indexUrl := fmt.Sprintf("%s/index.yml", url)
		content, err := LoadResource(indexUrl, true, []string{})
		if err != nil {
			log.Errorf("Failed to load %s: %v", indexUrl, err)
			continue
		}

		services := make(map[string][]string)
		err = yaml.Unmarshal(content, &services)
		if err != nil {
			log.Errorf("Failed to unmarshal %s: %v", indexUrl, err)
			continue
		}

		if list, ok := services["services"]; ok {
			result = append(result, list...)
		}
	}

	return result, nil
}

func LoadResource(location string, network bool, urls []string) ([]byte, error) {
	var bytes []byte
	err := ErrNotFound

	if strings.HasPrefix(location, "http:/") || strings.HasPrefix(location, "https:/") {
		if !network {
			return nil, ErrNoNetwork
		}
		resp, err := http.Get(location)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("non-200 http response: %d", resp.StatusCode)
		}
		defer resp.Body.Close()
		return ioutil.ReadAll(resp.Body)
	} else if strings.HasPrefix(location, "/") {
		return ioutil.ReadFile(location)
	} else if len(location) > 0 {
		for _, url := range urls {
			ymlUrl := fmt.Sprintf("%s/%s/%s.yml", url, location[0:1], location)
			bytes, err = LoadResource(ymlUrl, network, []string{})
			if err == nil {
				log.Debugf("Loaded %s from %s", location, ymlUrl)
				return bytes, nil
			}
		}
	}

	return nil, err
}

func GetValue(kvPairs []string, key string) string {
	if kvPairs == nil {
		return ""
	}

	prefix := key + "="
	for _, i := range kvPairs {
		if strings.HasPrefix(i, prefix) {
			return strings.TrimPrefix(i, prefix)
		}
	}

	return ""
}

func Map2KVPairs(m map[string]string) []string {
	r := make([]string, 0, len(m))
	for k, v := range m {
		r = append(r, k+"="+v)
	}
	return r
}

func KVPairs2Map(kvs []string) map[string]string {
	r := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		s := strings.SplitN(kv, "=", 2)
		r[s[0]] = s[1]
	}
	return r
}

func TrimSplitN(str, sep string, count int) []string {
	result := []string{}
	for _, part := range strings.SplitN(strings.TrimSpace(str), sep, count) {
		result = append(result, strings.TrimSpace(part))
	}

	return result
}

func TrimSplit(str, sep string) []string {
	return TrimSplitN(str, sep, -1)
}
