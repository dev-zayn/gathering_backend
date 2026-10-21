import { createContext, useContext } from 'react'

// Who is signed in: the admin and their roles, from GET /v1/admin/me.
export const Me = createContext(null)
export const useMe = () => useContext(Me)
