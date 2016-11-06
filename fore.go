// fore project fore.go
package main

import (
	"archive/zip"
	"bufio"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	//"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var status = ForeStatus{busy: false}
var (
	clientrun                  = 0
	howmanyserveroff           = 0
	consecutivedifferentchecks = 0
	lastknownlocalchange       time.Time
	lastknownremotechange      time.Time
	offset                     = 0
	path                       string
	localPort                  *int
)

const (
	LOCAL_HOST  = "0.0.0.0"
	LOCAL_PORT  = 5001
	REMOTE_HOST = "127.0.0.1"
	REMOTE_PORT = 5000
	FULLPERIOD  = 5

	HEADER  = "_fore"
	ZIPFILE = "_fore.zip"
	CHKFILE = "_foreck.txt"
	LOGFILE = "_forelog.txt"
	RLGFILE = "_forelog_rem.txt"

	SRV = "SRV:"
	CLI = "\t\t\tCLI:"
)

func main() {
	var wg sync.WaitGroup

	remoteHost := flag.String("rh", REMOTE_HOST, "remote host")
	remotePort := flag.Int("rp", REMOTE_PORT, "remote port")
	localHost := flag.String("lh", LOCAL_HOST, "local host")
	localPort = flag.Int("lp", LOCAL_PORT, "local port")
	flag.Parse()
	args := flag.Args()
	pathfound, err := filepath.Abs(args[0])
	path = pathfound
	if err != nil {
		fmt.Println("Cannot manage path ", args)
	}
	fmt.Println("FlagArgs:", path)
	fmt.Printf("Replicating %s with Remote host %s:%d from local address %s:%d\r\n", path, *remoteHost, *remotePort, *localHost, *localPort)
	fmt.Println("GoFoRe is starting...")
	if _, err = os.Stat(path + "/" + CHKFILE); os.IsNotExist(err) {
		fmt.Println(CHKFILE, "not found, creating")
		firstck := calculateCheckSumData("")
		writeSortedHashtable(firstck, CHKFILE, "")
		printHash(firstck, "Calculated checksums", "")
	}
	fmt.Println(SRV, "Starting server listener")
	wg.Add(1)
	go server(*localHost, *localPort, wg)
	fmt.Println(CLI, "Starting client job task")
	wg.Add(2)
	go client(*remoteHost, *remotePort, wg)
	wg.Wait()
}

func server(localHost string, localPort int, wg sync.WaitGroup) {
	defer wg.Done()
	fmt.Printf("%s starting management Handler on %s:%d\n", SRV, localHost, localPort)
	l, err := net.Listen("tcp", localHost+":"+strconv.FormatUint(uint64(localPort), 10))
	if err != nil {
		fmt.Println(SRV, "Error listening:", err.Error())
		return
	}
	// Close the listener before exiting
	defer l.Close()
	for {
		// Listen for an incoming connection.
		fmt.Println(SRV, "Going into accept", l.Addr().String())
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("SRV: Error accepting: ", err.Error())
			return
		}
		// Handle connections in a new goroutine.
		fmt.Println(SRV, "Connection accepted, managing")
		go mngDialogWithRemoteCaller(conn)
		fmt.Println(SRV, "Back into accepting on", localPort)
	}
	fmt.Println(SRV, "Exiting from manage")
}

func mngDialogWithRemoteCaller(conn net.Conn) {
	fmt.Println(SRV, "Enter mngLine")
	for {
		fmt.Println(SRV, "Waiting for a CR terminated line to be sent")
		setRT(conn, -1)
		message, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			fmt.Println(SRV, "Error reading TCP:", err.Error())
			break
		}
		fmt.Println(SRV, "Original message len =", len(message))
		message = purifyString(message)
		fmt.Println(SRV, "Purified message len =", len(message))

		fmt.Printf("%s Received '%s'\r\n", SRV, message)
		if len(message) == 0 {
			continue
		}
		fmt.Println(SRV, "Elaboration start")
		tobreak := elaborate(message, conn)
		fmt.Println(SRV, "Elaboration end, tobreak = ", tobreak)
		if tobreak {
			break
		}

	}
	fmt.Println(SRV, "Exiting mngDialogWithRemoteCaller")
	conn.Close()
}

func client(remoteHost string, remotePort int, wg sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case clock := <-ticker.C:
				fmt.Println(CLI, "Realign:", clock.String())
				esito := realign(remoteHost, remotePort)
				fmt.Println(CLI, "End:", time.Now())
				fmt.Println(CLI, "Esito realign=", esito)
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func realign(remoteHost string, remotePort int) int {
	offset %= 15
	time.Sleep(time.Second * time.Duration(offset))
	defer unlock(CLI)
	ok := status.goBusy()
	if !ok {
		fmt.Println(CLI, "Working on the server side, client round skipped")
		offset++
		return -3
	} else {
		fmt.Println(CLI, "Lock acquired")
	}
	fmt.Printf("%s Client run: %d\r\n", CLI, clientrun)

	conn, err := net.Dial("tcp", remoteHost+":"+strconv.FormatUint(uint64(remotePort), 10))
	if err != nil {
		fmt.Println(CLI, "Error connecting: ", err.Error())
		howmanyserveroff++
		return -2
	}
	defer conn.Close()
	isthere := issueTextCommand(conn, "HELLO")
	if strings.Contains(isthere, "NOANSWER") {
		fmt.Println(CLI, "NOANSWER from server")
		howmanyserveroff++
		offset += 2
		return -2
	}
	if clientrun%FULLPERIOD != 0 { // giro standard
		fmt.Println(CLI, "Standard realign, cr =", clientrun)
		issueZIPcommand(conn, "ZIP")
	} else {
		fmt.Println(CLI, "Full realign, cr =", clientrun)
		issueZIPcommand(conn, "FUL")
	}
	issueTextCommand(conn, "RIECCOMI")
	fmt.Println(CLI, "Realign completed")
	return 0
}

func issueTextCommand(conn net.Conn, cmd string) string {
	fmt.Println(CLI, "Issuing", cmd, "command")
	_, err := conn.Write([]byte(cmd + "\r\n"))
	if err != nil {
		fmt.Println(CLI, "Error writing: ", err.Error())
		return ""
	}
	var limit int = 10
	if cmd == "Bye." {
		limit = 1
	}

	setRT(conn, limit)
	message, err := bufio.NewReader(conn).ReadString('\n')
	setRT(conn, -1)
	if err != nil {
		fmt.Println(CLI, "Error reading TCP:", err.Error())
		return "NOANSWER"
	}
	fmt.Printf("%s message '%s' len = %d\r\n", CLI, strings.TrimSpace(message), len(message))
	return message
}

func answer(conn net.Conn, answer string) {
	fmt.Println(SRV, "Answering :"+answer)
	_, err := conn.Write([]byte(answer + "\r\n"))
	if err != nil {
		fmt.Println(SRV, "Error writing: ", err.Error())
	}
}

func calculateCheckSumData(prefix string) map[string]string {
	ck := make(map[string]string)
	addChecksumDataInHashtable(ck, path, prefix)
	return ck
}

func addChecksumDataInHashtable(ck map[string]string, folderAbsolutePath string, prefix string) {
	relativePath := ""
	if folderAbsolutePath != path {
		relativePath = folderAbsolutePath[len(path)+1:len(folderAbsolutePath)] + "/"
	}
	//fmt.Printf("%s Root = [%s], actual = [%s], relative = [%s]\r\n", prefix, path, folderAbsolutePath, relativePath)
	recpath, err := os.Open(folderAbsolutePath)
	if err != nil {
		fmt.Println(prefix, "Cannot open", folderAbsolutePath)
		return
	}
	defer recpath.Close()
	files, err := recpath.Readdir(-1)
	if err != nil {
		fmt.Println(prefix, "Cannot read dir", recpath.Name())
		return
	}
	for _, file := range files {
		if !file.IsDir() && file.Mode().IsRegular() {
			//fmt.Println(prefix, file.Name(), file.Size(), "bytes")
			if strings.HasPrefix(file.Name(), HEADER) {
				fmt.Println(prefix, file.Name(), "Reserved file, skipping")
				continue
			}
			nameToPut := relativePath + file.Name()
			computed, err := calculateCheckSumOfFile(folderAbsolutePath + "/" + file.Name())
			if err != nil {
				fmt.Println(prefix, "Cannot calc CRC for ", folderAbsolutePath+"/"+file.Name())
				continue
			}
			fmt.Printf("%s Putting [%s](%d)=[%s]\r\n", prefix, nameToPut, file.Size(), computed)
			ck[nameToPut] = computed
		}
		if file.IsDir() {
			newPathToInvestigate := folderAbsolutePath + "/" + file.Name()
			addChecksumDataInHashtable(ck, newPathToInvestigate, prefix)
		}
	}
}

func calculateCheckSumOfFile(filePath string) (string, error) {
	//Initialize an empty return string now in case an error has to be returned
	var returnCRC32String string = ""

	//Open the fhe file located at the given path and check for errors
	file, err := os.Open(filePath)
	if err != nil {
		return returnCRC32String, err
	}

	//Tell the program to close the file when the function returns
	defer file.Close()

	//Create the table with the given polynomial
	tablePolynomial := crc32.MakeTable(crc32.IEEE)

	//Open a new hash interface to write the file to
	hash := crc32.New(tablePolynomial)

	//Copy the file in the interface
	if _, err := io.Copy(hash, file); err != nil {
		return returnCRC32String, err
	}

	returnCRC32String = strconv.FormatUint(uint64(hash.Sum32()), 10)

	return returnCRC32String, nil
}

func writeSortedHashtable(ck map[string]string, checksumFileName string, prefix string) {
	v := getVectorOfSortedKeys(ck)
	createChecksumFile(v, ck, checksumFileName, false, prefix)
}

func getVectorOfSortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func createChecksumFile(v []string, ck map[string]string, checksumFileName string, cckonly bool, prefix string) string {
	if cckonly {
		checksumFileName = "<CKONLY>"
	}
	fmt.Println(prefix, "Creating file ", checksumFileName, "from hash of size", len(v), "/", len(ck))
	//Create the table with the given polynomial
	tablePolynomial := crc32.MakeTable(crc32.IEEE)
	//Open a new hash interface to write the file to
	hash := crc32.New(tablePolynomial)
	var f *os.File
	var err error
	if !cckonly {
		f, err = os.Create(path + "/" + checksumFileName)
		if err != nil {
			fmt.Println(prefix, "Cannot create checksum file :"+path+"/"+checksumFileName, err.Error())
			return ""
		}
		defer f.Close()
	}
	if len(ck) == 0 {
		fmt.Println(prefix, "Warning, empty map being written to", checksumFileName)
	}
	for _, key := range v {
		ckForThisFile := ck[key]
		line := fmt.Sprintf("%s=%s\r\n", key, ckForThisFile)
		_, err := hash.Write([]byte(line))
		if err != nil {
			fmt.Println(prefix, "Cannot write hash for line :"+line, err.Error())
			return ""
		}
		if !cckonly {
			_, err := f.Write([]byte(line))
			if err != nil {
				fmt.Println(prefix, "Cannot write checksum line :"+line, err.Error())
				return ""
			}
		}
	}
	return strconv.FormatUint(uint64(hash.Sum32()), 10)
}

func readChecksumFile(checksumFileName string, prefix string) map[string]string {
	res := make(map[string]string)
	file, err := os.Open(path + "/" + checksumFileName)
	if err != nil {
		fmt.Println(prefix, "Cannot open "+checksumFileName, err.Error())
		return res
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	fillMapFromScanner(res, scanner, prefix)
	return res
}

func fillMapFromScanner(tofill map[string]string, toscan *bufio.Scanner, prefix string) {
	fmt.Println(prefix, "scanning file/socket to get checksums")
	for toscan.Scan() {
		linea := toscan.Text()
		if linea == "END" {
			break
		}
		fmt.Printf("%s letta riga [%s]\r\n", prefix, linea)
		pezzi := strings.Split(linea, "=")
		fmt.Printf("%s Parti:%s/%s\r\n", prefix, pezzi[0], pezzi[1])
		tofill[pezzi[0]] = pezzi[1]
	}
	fmt.Println(prefix, "scanning ended,", len(tofill), "lines found")
}

func compareBeforeAndAfter(prima map[string]string, dopo map[string]string, prefix string) map[string]string {
	fmt.Println(prefix, "Comparing", prima, "with", dopo)
	results := make(map[string]string)
	for k, v := range dopo {
		val, ok := prima[k]
		if !ok {
			results[k] = "added"
		} else {
			if val != v {
				results[k] = "changed"
			}
		}
	}
	for k := range prima {
		_, ok := dopo[k]
		if !ok {
			results[k] = "deleted"
		}
	}
	fmt.Println(prefix, "result of compare=", results)
	return results
}

func writeZipToOutput(conn net.Conn, results map[string]string) {
	zw := zip.NewWriter(conn)
	defer zw.Close()
	for k, v := range results {
		switch {
		case v != "deleted":
			source := path + "/" + k
			fmt.Println(SRV, "Adding entry", source, "to ZIP structure on connection", conn.LocalAddr().String(), "-->", conn.RemoteAddr().String())
			writeEntryInZip(k, source, zw)
		}
	}
}

func writeEntryInZip(fileName string, source string, zw *zip.Writer) {
	sourceFile, err := os.Open(source)
	if err != nil {
		fmt.Println(SRV, "Cannot generate entry for ", source, err.Error())
		return
	}
	defer sourceFile.Close()
	fw, err := zw.Create(fileName)
	if err != nil {
		fmt.Println(SRV, "Cannot generate entry for "+fileName, err.Error())
		return
	}
	written, err := io.Copy(fw, sourceFile)
	if err != nil {
		fmt.Println(SRV, "Error writing entry", fileName, "from source", source, err.Error())
	} else {
		fmt.Println(SRV, "Written entry", fileName, "from source", source, written, "bytes")
	}
	err = zw.Flush()
	if err != nil {
		fmt.Println(SRV, "Cannot flush zipwriter", err.Error())
		return
	}
}

func manageRIAcommand(conn net.Conn) {
	dopo := calculateCheckSumData(SRV)
	writeSortedHashtable(dopo, CHKFILE, SRV)
	results := make(map[string]string)
	results[CHKFILE] = "log"
	writeZipToOutput(conn, results)
}

func elaborate(inputLine string, theos net.Conn) bool {
	tobreak := false
	fmt.Println(SRV, "Elaborating[", inputLine, "]")
	ok := status.goBusy()
	if !ok {
		fmt.Println(SRV, "Locked by client operations, answering NOANSWER")
		answer(theos, "NOANSWER")
		return false
	} else {
		fmt.Println(SRV, "Lock acquired")
	}
	defer unlock(SRV)
	if inputLine == "ZIP" {
		manageZIPcommand(theos, false)
	} else if inputLine == "RIA" {
		manageRIAcommand(theos)
	} else if inputLine == "FUL" {
		manageZIPcommand(theos, true)
	} else if inputLine == "WAIT" {
		time.Sleep(time.Duration(20) * time.Second)
		answer(theos, "You wrote "+inputLine+"\r\n")
	} else if strings.HasPrefix(inputLine, "CHECK") {
		fmt.Println(SRV, "CHECK Started ", time.Now())
		testck := calculateCheckSumData(SRV)
		v := getVectorOfSortedKeys(testck)
		l := createChecksumFile(v, testck, "", true, SRV)
		outputLine := fmt.Sprintf("SUM %s \r\n", l)
		answer(theos, outputLine)
	} else if inputLine == "Bye." {
		tobreak = true
	} else {
		outputLine := "You wrote " + inputLine
		answer(theos, outputLine)
	}
	return tobreak
}

func manageZIPcommand(s net.Conn, isfull bool) {
	fmt.Println(SRV, "Managing ZIP Command. FULL = ", isfull)
	prima := make(map[string]string)
	if !isfull {
		fmt.Println(SRV, "Reading previous checksum data")
		prima = readChecksumFile(CHKFILE, SRV)
	} else {
		fmt.Println(SRV, "Getting previous checksum from remote")
		scanner := bufio.NewScanner(s)
		fillMapFromScanner(prima, scanner, SRV)
	}
	printHash(prima, "Situation to use as baseline", SRV)
	dopo := calculateCheckSumData(SRV)
	printHash(dopo, "Current situation", SRV)
	results := compareBeforeAndAfter(prima, dopo, SRV)
	printHash(results, "The difference is", SRV)
	if !isfull && len(results) > 0 {
		fmt.Println(SRV, "Not FUL and at least one change, setting lastknownlocalchange to now")
		lastknownlocalchange = time.Now()
	}
	fmt.Println("Writing ")
	writeSortedHashtable(results, LOGFILE, SRV)
	writeSortedHashtable(dopo, CHKFILE, SRV)
	results[LOGFILE] = "log"
	writeZipToOutput(s, results)
}

func issueZIPcommand(iosr net.Conn, cmd string) {
	fmt.Println(CLI, "Entered issueZIPcommand with option", cmd, "clientrun =", clientrun)
	if cmd == "ZIP" {
		fmt.Println(CLI, "Issuing ZIP command")
		_, err := iosr.Write([]byte(cmd + "\r\n"))
		if err != nil {
			fmt.Println(CLI, "error issuing ZIP command ", err.Error())
			return
		}
	}
	if cmd == "FUL" {
		tmp := calculateCheckSumData(CLI)
		printHash(tmp, "Temporary check of local data", CLI)
		v := getVectorOfSortedKeys(tmp)
		mychecksum := createChecksumFile(v, tmp, "", true, CLI)
		hischecksum := issueTextCommand(iosr, "CHECK "+mychecksum)
		if strings.Contains(hischecksum, "SUM "+mychecksum) { // POTENZIALE BACO? SUM XYZ vs SUM XY???
			fmt.Println(CLI, "Already synchronized")
			consecutivedifferentchecks = 0
			clientrun++ // incremento il numero di giri, per non ricadere qui
			return
		} else {
			fmt.Println(CLI, "MY: "+mychecksum+" HIS: "+hischecksum)
			consecutivedifferentchecks++
		}
		/*
		 * il CHECK e' differente!
		 * strategia :
		 * se e' la prima volta non si fa nulla
		 * altrimenti cerco di capire se devo essere riallineato io o l'altro
		 */
		fmt.Println(CLI, "CHECK IS DIFFERENT! Times : ", consecutivedifferentchecks)
		if consecutivedifferentchecks == 1 {
			fmt.Println(CLI, "First time with differences, let's wait")
			clientrun++ // incremento il numero di giri, per non ricadere qui
			return
		} else {
			fmt.Println(CLI, "lastknownlocalchange=", lastknownlocalchange)
			fmt.Println(CLI, "lastknownremotechange=", lastknownremotechange)
			fmt.Println(CLI, "time.Time{}=", time.Time{})
			if lastknownlocalchange.After(lastknownremotechange) {
				fmt.Println(CLI, "local changes more recent than remotes, let it go")
				clientrun++
				return
			} else if (lastknownlocalchange == time.Time{} && lastknownremotechange == time.Time{}) {
				fmt.Println(CLI, "Virgin system, waiting for something to happen on one side")
				clientrun++
				return
			} else if lastknownremotechange.Sub(lastknownlocalchange).Seconds() < 60 {
				if consecutivedifferentchecks < 3 {
					fmt.Println(CLI, "changes too near, let it go")
					clientrun++ // incremento il numero di giri, per non ricadere qui
					return
				}
			}
		}
		_, err := iosr.Write([]byte(cmd + "\r\n"))
		if err != nil {
			fmt.Println(CLI, "Cannot write command", cmd, "to", iosr)
			return
		}
		fmt.Println(CLI, "lanciato :"+cmd)
		for _, fileName := range v {
			ck := tmp[fileName]
			output := fmt.Sprintf("%s=%s\r\n", fileName, ck)
			_, err := iosr.Write([]byte(output))
			if err != nil {
				fmt.Println(CLI, "Cannot write line <", output, "> to", iosr)
				return
			}
		}
		fmt.Println(CLI, "Issuing END")
		_, err = iosr.Write([]byte("END\r\n"))
		if err != nil {
			fmt.Println(CLI, "Cannot write END to", iosr)
			return
		}
	}

	written, err := readFromConnToFile(iosr, path+"/"+ZIPFILE, CLI)
	if err != nil {
		fmt.Println(CLI, "Cannot write temp file ", path+"/"+ZIPFILE, err.Error())
		return
	}
	fmt.Println(CLI, "Temp file written : ", written)
	clientrun++                              // incremento il numero di giri, visto che ho letto la risposta
	adesso := readChecksumFile(CHKFILE, CLI) // leggo il mio file dei checksum perche' devo aggiornarlo con le cose riallineate dal remoto
	//Unzip
	printHash(adesso, "Checksum file before unzip", CLI)
	unzip(path+"/"+ZIPFILE, path, adesso)
	printHash(adesso, "Checksum file after unzip", CLI)
	/* ora le cancellazioni */
	if cmd == "ZIP" || cmd == "FUL" {
		fmt.Println(CLI, "phase = deleting")
		hislog := readChecksumFile(RLGFILE, CLI)
		if cmd == "ZIP" { // solo x riallineamento differenziale
			if len(hislog) > 0 { // test cambiamenti sul server (= log vuoto o meno)
				lastknownremotechange = time.Now()
			}
		}
		for file, _ := range hislog {
			if hislog[file] == "deleted" {
				fileToDelete := path + "/" + file
				fmt.Println(CLI, "Deleting :", fileToDelete)
				err := os.Remove(fileToDelete)
				if err != nil {
					fmt.Println(CLI, "Cannot delete ", fileToDelete, err.Error())
				}
				delete(adesso, file)
			}
		}
		fmt.Println(CLI, "Writing checksum back")
		printHash(adesso, "Checksum file after unzip/delete", CLI)
		writeSortedHashtable(adesso, CHKFILE, CLI) /* riscrivo il mio checksum */
	}
}

func unzip(zipFile, destinationPath string, checksum map[string]string) error {
	r, err := zip.OpenReader(zipFile)
	if err != nil {
		fmt.Println(CLI, "Cannot open zipfile "+zipFile, err.Error())
		return err
	}
	defer r.Close()

	//Create the table with the given polynomial
	tablePolynomial := crc32.MakeTable(crc32.IEEE)

	//Open a new hash interface to write the file to
	hash := crc32.New(tablePolynomial)

	for _, entryInZipReader := range r.File {
		hash.Reset()
		fmt.Println(CLI, "Unzipping :", entryInZipReader.Name)
		zipEntryReader, err := entryInZipReader.Open()
		if err != nil {
			fmt.Println(CLI, "Cannot open file in ZIP"+entryInZipReader.Name, err.Error())
			return err
		}
		defer zipEntryReader.Close()
		name := entryInZipReader.Name
		if name == LOGFILE {
			name = RLGFILE
		}
		totalPath := filepath.Join(destinationPath, name)
		if entryInZipReader.FileInfo().IsDir() {
			os.MkdirAll(totalPath, entryInZipReader.Mode())
		} else {
			os.MkdirAll(filepath.Dir(totalPath), entryInZipReader.Mode())
			physicalFileToWrite, err := os.OpenFile(
				totalPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, entryInZipReader.Mode())
			if err != nil {
				fmt.Println(CLI, "Cannot expand file in ZIP "+totalPath, err.Error())
				return err
			}
			defer physicalFileToWrite.Close()

			_, err = io.Copy(physicalFileToWrite, zipEntryReader)
			if err != nil {
				fmt.Println(CLI, "Error unzipping ", totalPath, err.Error())
				return err
			}
			if !strings.HasPrefix(name, HEADER) {
				_, err := physicalFileToWrite.Seek(0, 0)
				if err != nil {
					fmt.Println(CLI, "Error resetting offset")
					return err
				}
				written, err := io.Copy(hash, physicalFileToWrite)
				if err != nil {
					fmt.Println(CLI, "Error hash ", totalPath, err.Error())
					return err
				} else {
					checksum[physicalFileToWrite.Name()] = strconv.FormatUint(uint64(hash.Sum32()), 10)
					fmt.Println(CLI, "Hashed ", totalPath, ":", written, "bytes")
				}
			}
		}
	}

	return nil
}

func safelyCopyReadCloser(destination string, source io.ReadCloser, mode os.FileMode) error {
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		fmt.Println(CLI, "Cannot create file "+destination, err.Error())
		return err
	}
	defer f.Close()
	written, err := io.Copy(f, source)
	if err != nil {
		fmt.Println(CLI, "Cannot fill file "+destination, err.Error())
		return err
	}
	fmt.Printf("%s %d bytes written into %s", CLI, written, destination)
	return nil
}

func purifyString(message string) string {
	s := strings.TrimSpace(message)
	s = strings.Replace(s, "\r", "", -1)
	s = strings.Replace(s, "\n", "", -1)
	s = strings.Replace(s, "\t", " ", -1)
	s = strings.TrimSpace(s)
	return s
}

func printHash(toprint map[string]string, headline, prefix string) {
	fmt.Println(prefix, headline)
	if len(toprint) == 0 {
		fmt.Println(prefix, "[EMPTY MAP]")
	} else {
		for k, v := range toprint {
			fmt.Printf("%s [%s] = [%s]\r\n", prefix, k, v)
		}
	}

}

func unlock(prefix string) {
	fmt.Println(prefix, "Releasing lock")
	status.goNotBusy()
}

func setRT(conn net.Conn, seconds int) {
	if seconds == -1 {
		conn.SetReadDeadline(time.Time{})
	} else {
		conn.SetReadDeadline(time.Now().Add(time.Second * time.Duration(seconds)))
	}
}

func readFromConnToFile(conn net.Conn, fileName string, prefix string) (int, error) {
	fmt.Println(CLI, "Filling file", fileName, "from", conn.LocalAddr().String(), "<-->", conn.RemoteAddr().String())
	wrres := 0
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Println(prefix, "error creating", fileName, err.Error())
	}
	defer file.Close()
	setRT(conn, 1)
	defer setRT(conn, -1)
	var buffer = make([]byte, 4096)
	for paused := 0; paused < 2; {
		bytesRead, err := conn.Read(buffer)
		fmt.Println(prefix, "round of reading :", bytesRead, "bytes")
		if err == nil || isTimeout(err) {
			if err != nil {
				fmt.Println(prefix, "err = ", err.Error())
				paused++
			} else {
				fmt.Println(prefix, "err = nil")
			}
			fmt.Println(prefix, bytesRead, "bytes read")
			bytesWritten, errw := file.Write(buffer[:bytesRead])
			if errw != nil {
				fmt.Println(prefix, "error writing", fileName, errw.Error())
				return wrres, errw
			} else {
				wrres += bytesWritten
				fmt.Println(prefix, bytesWritten, "bytes written to ", fileName)
			}
		} else {
			fmt.Println(prefix, "error reading zip stream", err.Error())
			return wrres, err
		}
	}
	fmt.Println(prefix, "finished writing of "+fileName)
	return wrres, nil
}

func isTimeout(err error) bool {
	type timeout interface {
		Timeout() bool
	}
	te, ok := err.(timeout)
	return ok && te.Timeout()
}
