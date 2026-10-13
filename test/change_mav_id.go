package main

import (
	"flag"
	"log"
	"math"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

func main() {
	// Command line flags
	targetAddr := flag.String("target", "10.41.10.2:14550", "Target Pixhawk address")
	listenAddr := flag.String("listen", ":14550", "Address to listen for MAVLink messages")
	newID := flag.Int("id", 2, "New MAV System ID (1-255)")
	timeout := flag.Duration("timeout", 10*time.Second, "Timeout for waiting response")
	flag.Parse()

	// Validate ID
	if *newID < 1 || *newID > 255 {
		log.Fatalf("Invalid MAV ID: %d (must be 1-255)", *newID)
	}

	log.Printf("Creating MAVLink connection...")
	log.Printf("  - Target Pixhawk: %s", *targetAddr)
	log.Printf("  - Listen address: %s", *listenAddr)

	// UDP Server to receive messages, UDP Client to send commands
	node, err := gomavlib.NewNode(gomavlib.NodeConf{
		Endpoints: []gomavlib.EndpointConf{
			gomavlib.EndpointUDPServer{Address: *listenAddr},   // Listen for heartbeat & responses
			gomavlib.EndpointUDPClient{Address: *targetAddr},   // Send commands to Pixhawk
		},
		Dialect:     common.Dialect,
		OutVersion:  gomavlib.V2,
		OutSystemID: 255, // GCS system ID
	})
	if err != nil {
		log.Fatalf("Failed to create MAVLink node: %v", err)
	}
	defer node.Close()

	log.Println("✓ MAVLink node created")

	// Wait for heartbeat
	log.Println("Waiting for heartbeat...")
	eventCh := node.Events()
	var pixhawkSystemID byte
	var gotHeartbeat bool

	timeoutCh := time.After(5 * time.Second)
	for {
		select {
		case event := <-eventCh:
			if frame, ok := event.(*gomavlib.EventFrame); ok {
				if _, ok := frame.Message().(*common.MessageHeartbeat); ok {
					pixhawkSystemID = frame.SystemID()
					gotHeartbeat = true
					log.Printf("✓ Got heartbeat from Pixhawk (System ID: %d)", pixhawkSystemID)
				}
			}
		case <-timeoutCh:
			log.Fatalf("❌ Timeout waiting for heartbeat from Pixhawk")
		}
		if gotHeartbeat {
			break
		}
	}


	// Send PARAM_SET message to change SYSID_THISMAV
	log.Printf("Sending PARAM_SET to change MAV_SYS_ID from %d to %d...", pixhawkSystemID, *newID)

	// PX4 uses bytewise encoding for parameters
	// We need to encode int32 bytes directly into float32 field
	paramValueEncoded := math.Float32frombits(uint32(*newID))

	paramMsg := &common.MessageParamSet{
		TargetSystem:    pixhawkSystemID,
		TargetComponent: 1, // Autopilot component
		ParamId:         "MAV_SYS_ID",  // PX4 parameter for System ID
		ParamValue:      paramValueEncoded,
		ParamType:       common.MAV_PARAM_TYPE_INT32,
	}

	err = node.WriteMessageAll(paramMsg)
	if err != nil {
		log.Fatalf("Failed to send PARAM_SET: %v", err)
	}

	log.Println("✓ PARAM_SET command sent")

	// Wait for PARAM_VALUE acknowledgment
	log.Println("Waiting for PARAM_VALUE response...")
	timeoutCh = time.After(*timeout)
	paramACKReceived := false
	eventCount := 0

	for {
		select {
		case event := <-eventCh:
			eventCount++
			if frame, ok := event.(*gomavlib.EventFrame); ok {
				switch msg := frame.Message().(type) {
				case *common.MessageParamValue:
					// Check if this is the response we're waiting for
					paramIDStr := msg.ParamId
					if paramIDStr == "MAV_SYS_ID" {
						// PX4 uses bytewise encoding - decode int32 from float32 bytes
						newValue := int32(math.Float32bits(msg.ParamValue))
						log.Printf("✓ PARAM_VALUE received:")
						log.Printf("  - Param ID: %s", paramIDStr)
						log.Printf("  - New value: %d", newValue)
						log.Printf("  - Param count: %d", msg.ParamCount)
						log.Printf("  - Param index: %d", msg.ParamIndex)

						if int(newValue) == *newID {
							log.Printf("✅ SUCCESS! MAV_SYS_ID changed to %d", newValue)
							paramACKReceived = true
						} else {
							log.Printf("⚠️  Value mismatch. Expected %d, got %d", *newID, newValue)
						}
						break
					}
				}
			}
		case <-timeoutCh:
			log.Printf("❌ Timeout waiting for PARAM_VALUE response (received %d events)", eventCount)
			break
		}
		if paramACKReceived {
			break
		}
	}

	if !paramACKReceived {
		log.Printf("⚠️  Did not receive confirmation, but command was sent")
		log.Printf("Note: You may need to reboot Pixhawk for changes to take effect")
	} else {
		log.Printf("\n✅ MAV_SYS_ID successfully changed!")
		log.Printf("Pixhawk may need to be rebooted for changes to persist")
	}
}
