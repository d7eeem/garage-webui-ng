import TabView, { Tab } from "@/components/containers/tab-view";
import Page from "@/context/page-context";
import { InfoIcon, UserIcon, UsersIcon } from "lucide-react";
import AboutTab from "./about-tab";
import AccountTab from "./account-tab";
import UsersTab from "./users-tab";

// One entry per settings section. TabView keys the visible tab off the `tab`
// search param, so adding a section is a single entry here plus its component —
// nothing else in this file needs to change.
//
// The Users tab is listed unconditionally; it renders its own "administrator
// access required" note for a viewer, and the API behind it is admin-only on
// the server regardless of what this array says.
const tabs: Tab[] = [
  {
    name: "account",
    title: "Account",
    icon: UserIcon,
    Component: AccountTab,
  },
  {
    name: "users",
    title: "Users",
    icon: UsersIcon,
    Component: UsersTab,
  },
  {
    name: "about",
    title: "About",
    icon: InfoIcon,
    Component: AboutTab,
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
