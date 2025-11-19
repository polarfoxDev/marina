import { useEffect, useState } from "react";
import { BrowserRouter, Routes, Route, Navigate, Link } from "react-router-dom";
import { SchedulesView } from "./views/SchedulesView";
import { JobStatusesView } from "./views/JobStatusesView";
import { JobDetailsView } from "./views/JobDetailsView";
import { SystemLogsView } from "./views/SystemLogsView";
import { LoginView } from "./views/LoginView";
import { Button } from "./components/ui/button";

function App() {
  const [authRequired, setAuthRequired] = useState<boolean | null>(null);
  const [authenticated, setAuthenticated] = useState(false);
  const [loading, setLoading] = useState(true);

  async function checkAuth() {
    try {
      const token = localStorage.getItem("marina_auth_token");
      const headers: HeadersInit = {};
      if (token) {
        headers["Authorization"] = `Bearer ${token}`;
      }
      const response = await fetch("/api/auth/check", { headers });
      if (response.ok) {
        const data = await response.json();
        setAuthRequired(data.authRequired);
        setAuthenticated(data.authenticated);
      }
    } catch (err) {
      console.error("Failed to check auth:", err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    checkAuth();
  }, []);

  async function handleLogout() {
    try {
      const { api } = await import("./api");
      await api.logout();
      setAuthenticated(false);
    } catch (err) {
      console.error("Logout failed:", err);
    }
  }

  function handleLoginSuccess() {
    setAuthenticated(true);
  }

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50">
        <div className="text-gray-500">Loading...</div>
      </div>
    );
  }

  // If auth is required but user is not authenticated, show login
  if (authRequired && !authenticated) {
    return (
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginView onLoginSuccess={handleLoginSuccess} />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      </BrowserRouter>
    );
  }

  return (
    <BrowserRouter>
      <div className="min-h-screen bg-gray-50">
        <header className="bg-white shadow-sm">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-8">
                <h1 className="text-2xl font-bold text-gray-900">
                  Marina Backup Status
                </h1>
                <nav className="flex space-x-4">
                  <Link
                    to="/"
                    className="text-sm text-gray-600 hover:text-gray-900 font-medium"
                  >
                    Schedules
                  </Link>
                  <Link
                    to="/logs"
                    className="text-sm text-gray-600 hover:text-gray-900 font-medium"
                  >
                    System Logs
                  </Link>
                </nav>
              </div>
              {authRequired && authenticated && (
                <Button variant="ghost" onClick={handleLogout}>
                  Logout
                </Button>
              )}
            </div>
          </div>
        </header>

        <main>
          <Routes>
            <Route path="/" element={<SchedulesView />} />
            <Route path="/logs" element={<SystemLogsView />} />
            <Route path="/instance/:instanceId" element={<JobStatusesView />} />
            <Route
              path="/instance/:instanceId/job/:jobId"
              element={<JobDetailsView />}
            />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default App;
