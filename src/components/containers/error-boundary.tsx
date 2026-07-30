import { Component, ErrorInfo, ReactNode } from "react";
import { Alert } from "react-daisyui";
import { CircleXIcon } from "lucide-react";

type Props = {
  children: ReactNode;
};

type State = {
  error: Error | null;
};

// React only supports error boundaries as class components; there is no hook
// equivalent. This is the one class component in the codebase.
class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Unhandled render error:", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="p-4 md:p-8">
          <Alert status="error" icon={<CircleXIcon />} className="items-start">
            <div>
              <p className="font-medium">Something went wrong.</p>
              <p className="text-sm opacity-80">{this.state.error.message}</p>
            </div>
          </Alert>
        </div>
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
