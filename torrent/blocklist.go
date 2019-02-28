package torrent

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/boltdb/bolt"
	"github.com/cenkalti/backoff"
)

func (s *Session) startBlocklistReloader() error {
	if s.config.BlocklistURL == "" {
		return nil
	}
	blocklistTimestamp, err := s.getBlocklistTimestamp()
	if err != nil {
		return err
	}

	s.mBlocklist.Lock()
	s.blocklistTimestamp = blocklistTimestamp
	s.mBlocklist.Unlock()

	deadline := blocklistTimestamp.Add(s.config.BlocklistUpdateInterval)
	now := time.Now()
	delta := now.Sub(deadline)
	var nextReload time.Duration
	if blocklistTimestamp.IsZero() {
		s.log.Infof("Blocklist is empty. Loading blocklist...")
		s.retryReloadBlocklist()
		nextReload = s.config.BlocklistUpdateInterval
	} else if deadline.Before(now) {
		s.log.Infof("Last blocklist reload was %s ago. Reloading blocklist...", delta.String())
		s.retryReloadBlocklist()
		nextReload = s.config.BlocklistUpdateInterval
	} else {
		s.log.Infof("Loading blocklist from session db...")
		err = s.loadBlocklistFromDB()
		if err != nil {
			return err
		}
		nextReload = deadline.Sub(now)
	}
	go s.blocklistReloader(nextReload)
	return nil
}

func (s *Session) getBlocklistTimestamp() (time.Time, error) {
	var t time.Time
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(sessionBucket)
		val := b.Get(blocklistTimestampKey)
		if val == nil {
			return nil
		}
		var err2 error
		t, err2 = time.Parse(time.RFC3339, string(val))
		return err2
	})
	return t, err
}

func (s *Session) retryReloadBlocklist() {
	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 0

	ticker := backoff.NewTicker(bo)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := s.reloadBlocklist()
			if err != nil {
				s.log.Errorln("cannot load blocklist:", err.Error())
				continue
			}
			return
		case <-s.closeC:
			return
		}
	}
}

func (s *Session) reloadBlocklist() error {
	req, err := http.NewRequest(http.MethodGet, s.config.BlocklistURL, nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.closeC:
			cancel()
		case <-ctx.Done():
		}
	}()
	req = req.WithContext(ctx)

	client := http.Client{
		Timeout: s.config.BlocklistUpdateTimeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errors.New("invalid blocklist status code")
	}

	var r io.Reader = resp.Body
	if resp.Header.Get("content-type") == "application/x-gzip" {
		gr, gerr := gzip.NewReader(r)
		if gerr != nil {
			return gerr
		}
		defer gr.Close()
		r = gr
	}

	buf := bytes.NewBuffer(make([]byte, 0, resp.ContentLength))
	r = io.TeeReader(r, buf)

	err = s.loadBlocklistReader(r)
	if err != nil {
		return err
	}

	now := time.Now()

	s.mBlocklist.Lock()
	s.blocklistTimestamp = now
	s.mBlocklist.Unlock()

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(sessionBucket)
		err2 := b.Put(blocklistKey, buf.Bytes())
		if err2 != nil {
			return err2
		}
		return b.Put(blocklistTimestampKey, []byte(now.Format(time.RFC3339)))
	})
}

func (s *Session) loadBlocklistFromDB() error {
	return s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(sessionBucket)
		val := b.Get(blocklistKey)
		if len(val) == 0 {
			return errors.New("no blocklist data in db")
		}
		return s.loadBlocklistReader(bytes.NewReader(val))
	})
}

func (s *Session) loadBlocklistReader(r io.Reader) error {
	n, err := s.blocklist.Reload(r)
	if err != nil {
		return err
	}
	s.log.Infof("Loaded %d rules from blocklist.", n)
	return nil
}

func (s *Session) blocklistReloader(d time.Duration) {
	for {
		select {
		case <-time.After(d):
		case <-s.closeC:
			return
		}

		s.log.Info("Reloading blocklist...")
		s.retryReloadBlocklist()
		d = s.config.BlocklistUpdateInterval
	}
}
