#include "PrecompET.h"
#include "ET_GoalManager.h"
#include "ET_NavigationFlags.h"

ET_GoalManager::ET_GoalManager()
{
	m_Instance = this;
}

ET_GoalManager::~ET_GoalManager()
{
	Shutdown();
}

void ET_GoalManager::CheckWaypointForGoal(Waypoint *_wp, BitFlag64 _used)
{
	enum { MaxGoals=10 };

	MapGoalDef Definition[MaxGoals];
	int NumDefs = 0;

	//////////////////////////////////////////////////////////////////////////

	// NumDefs 1
	if(_wp->IsFlagOn(F_ET_NAV_MG42SPOT))
	{
		Definition[NumDefs++].Props.SetString("Type","MOBILEMG42");
	}

	// NumDefs 2
	if(_wp->IsFlagOn(F_ET_NAV_MORTAR))
	{
		Definition[NumDefs++].Props.SetString("Type","MOBILEMORTAR");
	}

	// NumDefs 3
	if(_wp->IsFlagOn(F_ET_NAV_ARTSPOT))
	{
		Definition[NumDefs++].Props.SetString("Type","CALLARTILLERY");
	}

	// NumDefs 4
	if(_wp->IsFlagOn(F_ET_NAV_ARTYTARGET_S))
	{
		Definition[NumDefs++].Props.SetString("Type","ARTILLERY_S");
	}

	// NumDefs 5
	if(_wp->IsFlagOn(F_ET_NAV_ARTYTARGET_D))
	{
		Definition[NumDefs++].Props.SetString("Type","ARTILLERY_D");
	}

	// NumDefs 6
	if(_wp->IsFlagOn(F_ET_NAV_MINEAREA))
	{
		Definition[NumDefs++].Props.SetString("Type","PLANTMINE");
	}

	// NumDefs 7
	if(_wp->IsFlagOn(F_ET_NAV_CAPPOINT))
	{
		Definition[NumDefs++].Props.SetString("Type","cappoint");
	}

	// NumDefs 8
	if(_wp->IsFlagOn(F_ET_NAV_FLAMETHROWER))
	{
		Definition[NumDefs++].Props.SetString("Type","FLAME");
	}

	// NumDefs 9
	if(_wp->IsFlagOn(F_ET_NAV_PANZER))
	{
		Definition[NumDefs++].Props.SetString("Type","PANZER");
	}

	// NOTE: pay attention to MaxGoals / NumDefs!!

	RegisterWaypointGoals(_wp,Definition,NumDefs);
	
	// Allow the base class to process it.
	GoalManager::CheckWaypointForGoal(_wp, _used);
}
