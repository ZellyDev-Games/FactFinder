import { ChangeEvent, useEffect, useState } from "react";
import "./App.css";
import { repo } from "../wailsjs/go/models";
import Provider = repo.Provider;
import {
  GetFactProviders,
  OpenFactProviderFolder,
  SetReadPlan,
  SetEmulatorClient,
} from "../wailsjs/go/main/App";
import { EventsOn } from "../wailsjs/runtime";

enum EmulatorClient {
  RetroArch = "retroarch",
  NWA = "nwa",
  QUSB2SNES = "qusb2snes",
}

enum ConnectionStatus {
  Disconnected = 0,
  Connected = 1,
  Reconnecting = 2,
  WaitingForGame,
}

type ConnectionState = {
  connection_status: ConnectionStatus;
  message: string;
};

function App() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [emulatorState, setEmulatorState] = useState<Array<Array<string>>>([]);
  const [emulatorValues, setEmulatorValues] = useState<Array<Array<string>>>(
    [],
  );

  const [emulatorConnection, setEmulatorConnection] = useState<ConnectionState>(
    {
      connection_status: ConnectionStatus.Disconnected,
      message: "Emulator Not Found",
    },
  );

  const [openSplitConnection, setOpenSplitConnection] =
    useState<ConnectionState>({
      connection_status: ConnectionStatus.Disconnected,
      message: "Opensplit Not Found",
    }
    );

  const [selectedClient, setSelectedClient] = useState<EmulatorClient>(
    EmulatorClient.RetroArch,
  );

  useEffect(() => {
    return EventsOn("emulator:connection", (s: ConnectionState) => {
      setEmulatorConnection(s);
    });
  }, []);

  useEffect(() => {
    return EventsOn("opensplit:connection", (s: ConnectionState) => {
      setOpenSplitConnection(s);
    });
  }, []);

  useEffect(() => {
    return EventsOn("emulator:state", (s: Array<Array<string>>) => {
      setEmulatorState(s);
    });
  }, []);

  useEffect(() => {
    return EventsOn("emulator:values", (s: Array<Array<string>>) => {
      setEmulatorValues(s);
    });
  }, []);

  useEffect(() => {
    (async () => {
      setProviders(await GetFactProviders());
    })();
  }, []);

  const changeProvider = async (e: ChangeEvent<HTMLSelectElement>) => {
    await SetReadPlan(e.target.value);
  };

  const getStatusColor = (state: ConnectionStatus) => {
    switch (state) {
      case ConnectionStatus.Disconnected:
        return "red";
      case ConnectionStatus.Connected:
        return "#00FF00";
      case ConnectionStatus.Reconnecting:
        return "yellow";
      case ConnectionStatus.WaitingForGame:
        return "orange";
    }
  };

  return (
    <div id="App">
      <div style={{ marginTop: "10px", marginBottom: "10px" }}>
        <select
          value={selectedClient}
          onChange={async (e) => {
            const client = e.target.value as EmulatorClient;
            setSelectedClient(client);
            await SetEmulatorClient(client);
          }}
        >
          <option value={EmulatorClient.RetroArch}>RetroArch</option>
          <option value={EmulatorClient.NWA}>NWA</option>
          <option value={EmulatorClient.QUSB2SNES}>QUSB2SNES</option>
        </select>
      </div>
      <div>
        <select onChange={changeProvider}>
          <option value="">Select a Fact Provider</option>
          <option value="">---</option>
          {providers.map((provider: Provider) => (
            <option value={provider.FilePath}>{provider.Name}</option>
          ))}
        </select>
      </div>

      <div style={{ marginTop: "20px" }}>
        <button
          onClick={async () => {
            await OpenFactProviderFolder();
          }}
        >
          Open Fact Provider Folder
        </button>
      </div>
      <hr />
      <div
        style={{
          display: "flex",
          width: "100%",
          justifyContent: "center",
          marginTop: "20px",
        }}
        className="status"
      >
        <table>
          <tbody>
            <tr>
              <td>
                <div
                  style={{
                    backgroundColor: getStatusColor(
                      emulatorConnection.connection_status,
                    ),
                    borderRadius: "20px",
                    height: "15px",
                    width: "15px",
                  }}
                ></div>
              </td>
              <td>{emulatorConnection.message}</td>
            </tr>

            <tr>
              <td>
                <div
                  style={{
                    backgroundColor: getStatusColor(
                      openSplitConnection.connection_status,
                    ),
                    borderRadius: "20px",
                    height: "15px",
                    width: "15px",
                  }}
                ></div>
              </td>
              <td>{openSplitConnection.message}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <hr />
      <h5 style={{ textAlign: "left", marginBottom: 5 }}>State</h5>
      <div
        style={{
          display: "flex",
          justifyContent: "start",
          fontFamily: "monospace",
        }}
      >
        <table>
          <tbody>
            {emulatorState.map((s) => (
              <tr key={s[0]}>
                <td style={{ textAlign: "left" }}>{s[0]}</td>
                <td style={{ textAlign: "left" }}>{s[1]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h5 style={{ textAlign: "left", marginBottom: 5 }}>Watches</h5>
      <div style={{ fontFamily: "monospace" }}>
        <table>
          <tbody>
            {emulatorValues.map((s) => (
              <tr key={s[0]}>
                <td style={{ textAlign: "left" }}>{s[0]}</td>
                <td style={{ textAlign: "left" }}>{s[1]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default App;
