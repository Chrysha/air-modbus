package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/goburrow/modbus"
)

var (
	pm25      []float64
	avg       float32
	sum       float64
	waitlist  []float64
	offflag   bool
	waitflag  chan bool
	switchoff chan bool
	switchon  chan bool
)

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("Connect lost: %v", err)
}

func sub(client mqtt.Client) {
	topic := "air/+"
	token := client.Subscribe(topic, 1, nil)
	token.Wait()
	fmt.Printf("Subscribed to topic %s", topic)
}

func readAir() {
	//read mqtt data

	var broker = "192.168.178.55"
	var port = 1883
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, port))
	opts.SetClientID("go_client")

	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	mqttclient := mqtt.NewClient(opts)
	if token := mqttclient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	sub(mqttclient)
}

func handleAirData() {
	modbusclient := initModbus()
	for {
		select {
		case <-switchoff:
			fmt.Println("Switch off now")
			turnOffAir(modbusclient)
			offflag = true
		case <-switchon:
			fmt.Println("Switch on now")
			turnOnAir(modbusclient)
			offflag = false
		}
	}
}

//turn off
func turnOffAir(client modbus.Client) {

	//turn off 1017 and 1018
	results, err := client.WriteSingleRegister(1017, 0)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)

	results, err = client.WriteSingleRegister(1018, 0)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)
}

//turn on
func turnOnAir(client modbus.Client) {

	results, err := client.WriteSingleRegister(1017, 2)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)

	results, err = client.WriteSingleRegister(1018, 1)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)
}

func initModbus() modbus.Client {
	// Modbus TCP
	handler := modbus.NewTCPClientHandler("192.168.178.33:502")
	handler.Timeout = 10 * time.Second
	handler.SlaveId = 0xFF
	err := handler.Connect()
	defer handler.Close()
	client := modbus.NewClient(handler)

	// Read holding register 1017 Tag and 1018 Nacht
	tag, err := client.ReadHoldingRegisters(1017, 1)
	if err != nil {
		fmt.Println(err)
	}

	nacht, err := client.ReadHoldingRegisters(1018, 1)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(tag)
	fmt.Println(nacht)

	if tag[1] == 0 || nacht[1] == 0 {
		offflag = true
	} else {
		offflag = false
	}
	fmt.Println("offflag", offflag)

	return client
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	sum = 0
	fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
	if msg.Topic() == "air/pm25" {
		fmt.Println("Topic is pm25")
		pmvalue, _ := strconv.ParseFloat(string(msg.Payload()), 64)

		if !offflag {
			// 180 values / 30 min
			if len(pm25) <= 200 {
				pm25 = append(pm25, pmvalue)
			} else {
				pm25 = pm25[1:]
				pm25 = append(pm25, pmvalue)
			}
			fmt.Println("Current Value List:", pm25)
		}

		for i := 0; i < len(pm25); i++ {
			sum += pm25[i]
		}
		if len(pm25) == 0 {
			fmt.Println("Len was 0")
			avg = float32(pmvalue)
		} else {
			avg = (float32(sum)) / (float32(len(pm25)))
		}

		fmt.Println("Average of pm25:", avg)

		//float32(pmvalue) >= avg*2 && !offflag
		if float32(pmvalue) >= avg+8 && !offflag {
			fmt.Println("Turn Air flow off")
			switchoff <- true
		} else if float32(pmvalue) < avg+6 && offflag { // avg * 1.2
			fmt.Println("Get ready to Turn Air flow on")
			waitlist = append(waitlist, pmvalue)
			fmt.Println("Waitlist is:", waitlist)
		} else if float32(pmvalue) >= avg+6 && offflag {
			waitlist = nil
			fmt.Println("Waitlist is nil", waitlist)
		}
		if len(waitlist) == 60 {
			fmt.Println("Turn Air flow on")
			switchon <- true
			waitlist = nil
		}
	}
}

func main() {
	s := make(chan os.Signal)
	switchoff = make(chan bool)
	switchon = make(chan bool)
	waitflag = make(chan bool)
	avg = 15.0

	signal.Notify(s, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-s
		// add function
		os.Exit(1)
	}()
	initModbus()
	go readAir()
	go handleAirData()
	for {

	}

}
