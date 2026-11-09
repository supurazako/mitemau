import "./App.css";
import { useEffect, useState } from "react";

function App() {
  const [message, setMessage] = useState("Not connected yet");
  useEffect(() => {
    let ignore = false;

    async function fetchHealth() {
      try {
        const response = await fetch("/api/health");
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }

        const text = await response.text();
        if (!ignore) {
          setMessage(text.trim());
        }
      } catch {
        if (!ignore) {
          setMessage("Failed to connect to the API");
        }
      }
    }

    fetchHealth();

    return () => {
      ignore = true;
    };
  }, []);
  return (
    <main>
      <h1>API connection</h1>
      <p>{message}</p>
    </main>
  );
}

export default App;
