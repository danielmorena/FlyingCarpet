package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#cgo LDFLAGS: -framework CoreWLAN
#import <Foundation/Foundation.h>
#import <CoreWLAN/CoreWLAN.h>

int startAdHoc(char * cSSID, char * cPassword) {
	NSString * SSID = [[NSString alloc] initWithUTF8String:cSSID];
	NSString * password = [[NSString alloc] initWithUTF8String:cPassword];
	CWInterface * iface = CWWiFiClient.sharedWiFiClient.interface;
	NSError *ibssErr = nil;
	BOOL result = [iface startIBSSModeWithSSID:[SSID dataUsingEncoding:NSUTF8StringEncoding] security:kCWIBSSModeSecurityNone channel:11 password:password error:&ibssErr];
	// NSLog(@"%d", result);
	return result;
}
int joinAdHoc(char * cSSID, char * cPassword) {
	NSString * SSID = [[NSString alloc] initWithUTF8String:cSSID];
	NSString * password = [[NSString alloc] initWithUTF8String:cPassword];
	CWInterface * iface = CWWiFiClient.sharedWiFiClient.interface;
	NSError * ibssErr = nil;
	NSSet<CWNetwork *> * network = [iface scanForNetworksWithName:SSID error:&ibssErr];
	BOOL result = [iface associateToNetwork:network.anyObject password:password error:&ibssErr];
	NSLog(@"%d", result);
	return result;
}
*/
import "C"
import (
	"fmt"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func connectToPeer(t *Transfer) (err error) {

	if t.Mode == "sending" {
		if err = checkForFile(t); err != nil {
			return errors.New("Could not find file to send: " + t.Filepath)
		}
		if err = joinAdHoc(t); err != nil {
			return
		}
		go stayOnAdHoc(t)
		if t.Peer == "mac" {
			t.RecipientIP, err = findMac(t)
			if err != nil {
				return
			}
		} else if t.Peer == "windows" {
			t.RecipientIP = findWindows(t)
		} else if t.Peer == "linux" {
			t.RecipientIP = findLinux(t)
		}
	} else if t.Mode == "receiving" {
		if t.Peer == "windows" || t.Peer == "linux" {
			if err = joinAdHoc(t); err != nil {
				return
			}
			go stayOnAdHoc(t)
		} else if t.Peer == "mac" {
			if err = startAdHoc(t); err != nil {
				return
			}
		}
	}
	return
}

func startAdHoc(t *Transfer) (err error) {

	ssid := C.CString(t.SSID)
	password := C.CString(t.Passphrase + t.Passphrase)
	var cRes C.int = C.startAdHoc(ssid, password)
	res := int(cRes)

	C.free(unsafe.Pointer(ssid))
	C.free(unsafe.Pointer(password))

	if res == 1 {
		t.output("SSID " + t.SSID + " started.")
		return
	} else {
		return errors.New("Failed to start ad hoc network.")
	}
}

func joinAdHoc(t *Transfer) (err error) {
	t.output("Looking for ad-hoc network " + t.SSID + " for " + strconv.Itoa(JOIN_ADHOC_TIMEOUT) + " seconds...")
	timeout := JOIN_ADHOC_TIMEOUT
	ssid := C.CString(t.SSID)
	password := C.CString(t.Passphrase + t.Passphrase)
	var cRes C.int = C.joinAdHoc(ssid, password)
	res := int(cRes)

	for res == 0 {
		select {
		case <-t.Ctx.Done():
			return errors.New("Exiting joinAdHoc, transfer was canceled.")
		default:
			if timeout <= 0 {
				return errors.New("Could not find the ad hoc network within " + strconv.Itoa(JOIN_ADHOC_TIMEOUT) + " seconds.")
			}
			// t.output(fmt.Sprintf("Failed to join the ad hoc network. Trying for %2d more seconds.", timeout))
			timeout -= 5
			time.Sleep(time.Second * time.Duration(3))
			res = int(C.joinAdHoc(ssid, password))
		}
	}
	return
}

func getCurrentWifi(t *Transfer) (SSID string) {
	cmdStr := "/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport -I | awk '/ SSID/ {print substr($0, index($0, $2))}'"
	SSID = runCommand(cmdStr)
	return
}

func getWifiInterface() string {
	getInterfaceString := "networksetup -listallhardwareports | awk '/Wi-Fi/{getline; print $2}'"
	return runCommand(getInterfaceString)
}

func getIPAddress(t *Transfer) string {
	var currentIP string
	t.output("Waiting for local IP...")
	for currentIP == "" {
		currentIPString := "ipconfig getifaddr " + getWifiInterface()
		currentIPBytes, err := exec.Command("sh", "-c", currentIPString).CombinedOutput()
		if err != nil {
			time.Sleep(time.Second * time.Duration(3))
			continue
		}
		currentIP = strings.TrimSpace(string(currentIPBytes))
	}
	t.output(fmt.Sprintf("Wi-Fi interface IP found: %s", currentIP))
	return currentIP
}

func findMac(t *Transfer) (peerIP string, err error) {
	timeout := FIND_MAC_TIMEOUT
	currentIP := getIPAddress(t)
	pingString := "ping -c 5 169.254.255.255 | " + // ping broadcast address
		"grep --line-buffered -oE '[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}' | " + // get all IPs
		"grep --line-buffered -vE '169.254.255.255' | " + // exclude broadcast address
		"grep -vE '" + currentIP + "'" // exclude current IP

	t.output("Looking for peer IP for " + strconv.Itoa(FIND_MAC_TIMEOUT) + " seconds.")
	for peerIP == "" {
		select {
		case <-t.Ctx.Done():
			return errors.New("Exiting dialPeer, transfer was canceled.")
		default:
			if timeout <= 0 {
				return "", errors.New("Could not find the peer computer within " + strconv.Itoa(FIND_MAC_TIMEOUT) + " seconds.")
			}
			pingBytes, pingErr := exec.Command("sh", "-c", pingString).CombinedOutput()
			if pingErr != nil {
				// t.output(fmt.Sprintf("Could not find peer. Waiting %2d more seconds. %s", timeout, pingErr))
				timeout -= 2
				time.Sleep(time.Second * time.Duration(2))
				continue
			}
			peerIPs := string(pingBytes)
			peerIP = peerIPs[:strings.Index(peerIPs, "\n")]
		}
	}
	t.output(fmt.Sprintf("Peer IP found: %s", peerIP))
	return
}

func findWindows(t *Transfer) (peerIP string) {
	currentIP := getIPAddress(t)
	if strings.Contains(currentIP, "192.168.137") {
		return "192.168.137.1"
	} else {
		return "192.168.173.1"
	}
}

func findLinux(t *Transfer) (peerIP string) {
	// timeout := FIND_MAC_TIMEOUT
	// currentIP := getIPAddress(t)
	// pingString := "ping -b -c 5 $(ifconfig | awk '/" + getWifiInterface() + "/ {for(i=1; i<=3; i++) {getline;}; print $6}') 2>&1 | " + // ping broadcast address
	// 	"grep --line-buffered -oE '[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}' | " + // get all IPs
	// 	"grep --line-buffered -vE $(ifconfig | awk '/" + getWifiInterface() + "/ {for(i=1; i<=3; i++) {getline;}; print $6}') | " + // exclude broadcast address
	// 	"grep -vE '" + currentIP + "'" // exclude current IP

	// t.output("Looking for peer IP for " + strconv.Itoa(FIND_MAC_TIMEOUT) + " seconds.")
	// for peerIP == "" {
	// 	if timeout <= 0 {
	// 		t.output("Could not find the peer computer within " + strconv.Itoa(FIND_MAC_TIMEOUT) + " seconds.")
	// 		return "", false
	// 	}
	// 	pingBytes, pingErr := exec.Command("sh", "-c", pingString).CombinedOutput()
	// 	if pingErr != nil {
	// 		t.output(fmt.Sprintf("Could not find peer. Waiting %2d more seconds. %s", timeout, pingErr))
	// 		t.output(fmt.Sprintf("peer IP: %s",string(pingBytes)))
	// 		timeout -= 2
	// 		time.Sleep(time.Second * time.Duration(2))
	// 		continue
	// 	}
	// 	peerIPs := string(pingBytes)
	// 	peerIP = peerIPs[:strings.Index(peerIPs, "\n")]
	// }
	// t.output(fmt.Sprintf("Peer IP found: %s", peerIP))
	// success = true
	// return
	return "10.42.0.1"
}

func resetWifi(t *Transfer) {
	wifiInterface := getWifiInterface()
	cmdString := "networksetup -setairportpower " + wifiInterface + " off && networksetup -setairportpower " + wifiInterface + " on"
	t.output(runCommand(cmdString))
	if t.Peer == "windows" || t.Peer == "linux" || t.Mode == "sending" {
		cmdString = "networksetup -removepreferredwirelessnetwork " + wifiInterface + " " + t.SSID
		t.output(runCommand(cmdString))
	}
}

func stayOnAdHoc(t *Transfer) {

	for {
		select {
		case <-t.Ctx.Done():
			t.output("Stopping ad hoc connection.")
			return
		default:
			if getCurrentWifi(t) != t.SSID {
				joinAdHoc(t)
			}
			time.Sleep(time.Second * 3)
		}
	}
}

func checkForFile(t *Transfer) (err error) {
	_, err = os.Stat(t.Filepath)
	return
}

func runCommand(cmd string) (output string) {
	cmdBytes, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(cmdBytes))
}

func getCurrentUUID(t *Transfer) (uuid string) { return "" }
