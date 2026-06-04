#include "PrecompRTCW.h"
#include "RTCW_GoalManager.h"
#include "RTCW_NavigationFlags.h"

RTCW_GoalManager::RTCW_GoalManager()
{
	m_Instance = this;
}

RTCW_GoalManager::~RTCW_GoalManager()
{
	Shutdown();
}

void RTCW_GoalManager::CheckWaypointForGoal(Waypoint *_wp, BitFlag64 _used)
{
	enum { MaxGoals=8 };

	MapGoalDef Definition[MaxGoals];
	int NumDefs = 0;

	//////////////////////////////////////////////////////////////////////////

	// NumDefs 1
	if(_wp->IsFlagOn(F_RTCW_NAV_ARTSPOT))
	{
		Definition[NumDefs++].Props.SetString("Type","CALLARTILLERY");
	}

	// NumDefs 2
	if(_wp->IsFlagOn(F_RTCW_NAV_ARTYTARGET_S))
	{
		Definition[NumDefs++].Props.SetString("Type","ARTILLERY_S");
	}

	// NumDefs 3
	if(_wp->IsFlagOn(F_RTCW_NAV_ARTYTARGET_D))
	{
		Definition[NumDefs++].Props.SetString("Type","ARTILLERY_D");
	}

	// NumDefs 4
	if(_wp->IsFlagOn(F_RTCW_NAV_CAPPOINT))
	{
		Definition[NumDefs++].Props.SetString("Type","cappoint");
	}

	// NumDefs 5
	if(_wp->IsFlagOn(F_RTCW_NAV_PANZER))
	{
		Definition[NumDefs++].Props.SetString("Type","PANZER");
	}

	// NumDefs 6
	if(_wp->IsFlagOn(F_RTCW_NAV_VENOM))
	{
		Definition[NumDefs++].Props.SetString("Type","VENOM");
	}

	// NumDefs 7
	if(_wp->IsFlagOn(F_RTCW_NAV_FLAMETHROWER))
	{
		Definition[NumDefs++].Props.SetString("Type","FLAME");
	}

	// NOTE: pay attention to MaxGoals / NumDefs!!

	RegisterWaypointGoals(_wp,Definition,NumDefs);
	
	// Allow the base class to process it.
	GoalManager::CheckWaypointForGoal(_wp, _used);
}
