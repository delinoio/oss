// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext } from "react";
// Presentation ownership only; source validators and mutation controls stay original.
export const GroupedTool = createContext(false);
export const useGroupedTool = () => useContext(GroupedTool);
