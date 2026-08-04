import TabView, { Tab } from "@/components/containers/tab-view";
import Page from "@/context/page-context";
import { UserIcon } from "lucide-react";
import AccountTab from "./account-tab";

// One entry per settings section. TabView keys the visible tab off the `tab`
// search param, so adding a section is a single entry here plus its component —
// nothing else in this file needs to change.
const tabs: Tab[] = [
  {
    name: "account",
    title: "Account",
    icon: UserIcon,
    Component: AccountTab,
  },
];

const SettingsPage = () => {
  return (
    <>
      <Page title="Settings" />

      <div className="container">
        <TabView tabs={tabs} className="bg-base-100 h-14 px-1.5" />
      </div>
    </>
  );
};

export default SettingsPage;
