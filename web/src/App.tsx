import { BrowserRouter, Routes, Route } from "react-router-dom";
import { SchedulesView } from "./components/SchedulesView";
import { JobStatusesView } from "./components/JobStatusesView";
import { JobDetailsView } from "./components/JobDetailsView";

function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen bg-gray-50">
        <header className="bg-white shadow-sm">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4">
            <h1 className="text-2xl font-bold text-gray-900">
              Marina Backup Status
            </h1>
          </div>
        </header>

        <main>
          <Routes>
            <Route path="/" element={<SchedulesView />} />
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
