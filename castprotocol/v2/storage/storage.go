package storage

import (
	"encoding/json"
	"io/ioutil"
	"os"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
)

var possibleCachePaths = []string{
	".config/gochromecast",
	".gochromecast",
}

type Storage struct {
	cache         map[string][]byte
	cacheFilename string
}

func NewStorage() *Storage {
	return &Storage{cache: map[string][]byte{}}
}

func (s *Storage) lazyLoadCacheDir() error {
	if s.cacheFilename != "" {
		return nil
	}

	homeDir, err := homedir.Dir()
	if err != nil {
		return errors.Wrap(err, "unable to find homedir")
	}
	for _, p := range possibleCachePaths {
		filename := homeDir + "/" + p

		// Check if file exists, if so then load it
		if _, err := os.Stat(filename); err == nil {
			s.cacheFilename = filename
			if fileContents, err := ioutil.ReadFile(filename); err == nil {
				if err := json.Unmarshal(fileContents, &s.cache); err == nil {
					return nil
				}
			}
		}

		// Attempt to create file.
		if _, err := os.Create(filename); err == nil {
			s.cacheFilename = filename
			return nil
		}

	}
	return errors.New("unable to create cache file")
}

func (s *Storage) Save(key string, data []byte) error {
	if err := s.lazyLoadCacheDir(); err != nil {
		return err
	}

	s.cache[key] = data

	cacheJson, _ := json.Marshal(s.cache)
	ioutil.WriteFile(s.cacheFilename, cacheJson, 0644)

	return nil
}

func (s *Storage) Load(key string) ([]byte, error) {
	if err := s.lazyLoadCacheDir(); err != nil {
		return nil, err
	}
	return s.cache[key], nil
}
